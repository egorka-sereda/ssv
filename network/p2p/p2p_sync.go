package p2pv1

import (
	"encoding/hex"
	"github.com/multiformats/go-multistream"
	"math/rand"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectypes "github.com/bloxapp/ssv-spec/types"
	libp2pnetwork "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2p_protocol "github.com/libp2p/go-libp2p/core/protocol"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/protocol/v2/message"
	p2pprotocol "github.com/bloxapp/ssv/protocol/v2/p2p"
)

func (n *p2pNetwork) SyncHighestDecided(mid spectypes.MessageID) error {
	go func() {
		logger := n.logger.With(zap.String("identifier", mid.String()))
		lastDecided, err := n.LastDecided(mid)
		if err != nil {
			logger.Debug("highest decided: sync failed", zap.Error(err))
			return
		}
		if len(lastDecided) == 0 {
			logger.Debug("highest decided: no messages were synced")
			return
		}
		results := p2pprotocol.SyncResults(lastDecided)
		results.ForEachSignedMessage(func(m *specqbft.SignedMessage) {
			raw, err := m.Encode()
			if err != nil {
				logger.Warn("could not encode signed message")
				return
			}
			n.msgRouter.Route(spectypes.SSVMessage{
				MsgType: spectypes.SSVConsensusMsgType,
				MsgID:   mid,
				Data:    raw,
			})
		})
	}()

	return nil
}

func (n *p2pNetwork) SyncDecidedByRange(identifier spectypes.MessageID, to, from specqbft.Height) {
	//TODO implement me
	//panic("implement me")
}

// LastDecided fetches last decided from a random set of peers
func (n *p2pNetwork) LastDecided(mid spectypes.MessageID) ([]p2pprotocol.SyncResult, error) {
	if !n.isReady() {
		return nil, p2pprotocol.ErrNetworkIsNotReady
	}
	pid, peerCount := n.fork.ProtocolID(p2pprotocol.LastDecidedProtocol)
	peers, err := n.getSubsetOfPeers(mid.GetPubKey(), peerCount, allPeersFilter)
	if err != nil {
		return nil, errors.Wrap(err, "could not get subset of peers")
	}
	return n.makeSyncRequest(peers, mid, pid, &message.SyncMessage{
		Params: &message.SyncParams{
			Identifier: mid,
		},
		Protocol: message.LastDecidedType,
	})
}

// GetHistory sync the given range from a set of peers that supports history for the given identifier
func (n *p2pNetwork) GetHistory(mid spectypes.MessageID, from, to specqbft.Height, targets ...string) ([]p2pprotocol.SyncResult, specqbft.Height, error) {
	if from >= to {
		return nil, 0, nil
	}

	if !n.isReady() {
		return nil, 0, p2pprotocol.ErrNetworkIsNotReady
	}
	protocolID, peerCount := n.fork.ProtocolID(p2pprotocol.DecidedHistoryProtocol)
	peers := make([]peer.ID, 0)
	for _, t := range targets {
		p, err := peer.Decode(t)
		if err != nil {
			continue
		}
		peers = append(peers, p)
	}
	// if no peers were provided -> select a random set of peers
	if len(peers) == 0 {
		random, err := n.getSubsetOfPeers(mid.GetPubKey(), peerCount, n.peersWithProtocolsFilter(string(protocolID)))
		if err != nil {
			return nil, 0, errors.Wrap(err, "could not get subset of peers")
		}
		peers = random
	}
	maxBatchRes := specqbft.Height(n.cfg.MaxBatchResponse)

	var results []p2pprotocol.SyncResult
	var err error
	currentEnd := to
	if to-from > maxBatchRes {
		currentEnd = from + maxBatchRes
	}
	results, err = n.makeSyncRequest(peers, mid, protocolID, &message.SyncMessage{
		Params: &message.SyncParams{
			Height:     []specqbft.Height{from, currentEnd},
			Identifier: mid,
		},
		Protocol: message.DecidedHistoryType,
	})
	if err != nil {
		return results, 0, err
	}
	return results, currentEnd, nil
}

// RegisterHandlers registers the given handlers
func (n *p2pNetwork) RegisterHandlers(handlers ...*p2pprotocol.SyncHandler) {
	m := make(map[libp2p_protocol.ID][]p2pprotocol.RequestHandler)
	for _, handler := range handlers {
		pid, _ := n.fork.ProtocolID(handler.Protocol)
		current, ok := m[pid]
		if !ok {
			current = make([]p2pprotocol.RequestHandler, 0)
		}
		current = append(current, handler.Handler)
		m[pid] = current
	}

	for pid, phandlers := range m {
		n.registerHandlers(pid, phandlers...)
	}
}

func (n *p2pNetwork) registerHandlers(pid libp2p_protocol.ID, handlers ...p2pprotocol.RequestHandler) {
	handler := p2pprotocol.CombineRequestHandlers(handlers...)
	n.host.SetStreamHandler(pid, func(stream libp2pnetwork.Stream) {
		req, respond, done, err := n.streamCtrl.HandleStream(stream)
		defer done()
		if err != nil {
			n.logger.Debug("could not handle stream", zap.Error(err))
			return
		}
		smsg, err := n.fork.DecodeNetworkMsg(req)
		if err != nil {
			n.logger.Debug("could not decode msg from stream", zap.Error(err))
			return
		}
		result, err := handler(smsg)
		if err != nil {
			n.logger.Debug("could not handle msg from stream", zap.Error(err))
			return
		}
		resultBytes, err := n.fork.EncodeNetworkMsg(result)
		if err != nil {
			n.logger.Debug("could not encode msg", zap.Error(err))
			return
		}
		if err := respond(resultBytes); err != nil {
			n.logger.Debug("could not respond to stream", zap.Error(err))
			return
		}
	})
}

// getSubsetOfPeers returns a subset of the peers from that topic
func (n *p2pNetwork) getSubsetOfPeers(vpk spectypes.ValidatorPK, peerCount int, filter func(peer.ID) bool) (peers []peer.ID, err error) {
	var ps []peer.ID
	seen := make(map[peer.ID]struct{})
	topics := n.fork.ValidatorTopicID(vpk)
	for _, topic := range topics {
		ps, err = n.topicsCtrl.Peers(topic)
		if err != nil {
			continue
		}
		for _, p := range ps {
			if _, ok := seen[p]; !ok && filter(p) {
				peers = append(peers, p)
				seen[p] = struct{}{}
			}
		}
	}
	// if we seen some peers, ignore the error
	if err != nil && len(seen) == 0 {
		return nil, errors.Wrapf(err, "could not read peers for validator %s", hex.EncodeToString(vpk))
	}
	if len(peers) == 0 {
		n.logger.Debug("could not find peers", zap.Any("topics", topics))
		return nil, nil
	}
	if peerCount > len(peers) {
		peerCount = len(peers)
	} else {
		rand.Shuffle(len(peers), func(i, j int) { peers[i], peers[j] = peers[j], peers[i] })
	}
	return peers[:peerCount], nil
}

func (n *p2pNetwork) makeSyncRequest(peers []peer.ID, mid spectypes.MessageID, protocol libp2p_protocol.ID, syncMsg *message.SyncMessage) ([]p2pprotocol.SyncResult, error) {
	var results []p2pprotocol.SyncResult
	data, err := syncMsg.Encode()
	if err != nil {
		return nil, errors.Wrap(err, "could not encode sync message")
	}
	msg := &spectypes.SSVMessage{
		MsgType: message.SSVSyncMsgType,
		MsgID:   mid,
		Data:    data,
	}
	encoded, err := n.fork.EncodeNetworkMsg(msg)
	if err != nil {
		return nil, err
	}
	plogger := n.logger.With(zap.String("protocol", string(protocol)), zap.String("identifier", mid.String()))
	msgID := n.fork.MsgID()
	distinct := make(map[string]bool)
	for _, pid := range peers {
		logger := plogger.With(zap.String("peer", pid.String()))
		raw, err := n.streamCtrl.Request(pid, protocol, encoded)
		if err != nil {
			if err != multistream.ErrNotSupported {
				logger.Debug("could not make stream request", zap.Error(err))
			}
			continue
		}
		mid := msgID(raw)
		if distinct[mid] {
			continue
		}
		distinct[mid] = true
		res, err := n.fork.DecodeNetworkMsg(raw)
		if err != nil {
			logger.Debug("could not decode stream response", zap.Error(err))
			continue
		}
		results = append(results, p2pprotocol.SyncResult{
			Msg:    res,
			Sender: pid.String(),
		})
	}
	return results, nil
}

// peersWithProtocolsFilter is used to accept peers that supports the given protocols
func (n *p2pNetwork) peersWithProtocolsFilter(protocols ...string) func(peer.ID) bool {
	return func(id peer.ID) bool {
		supported, err := n.host.Network().Peerstore().SupportsProtocols(id, protocols...)
		if err != nil {
			// TODO: log/trace error
			return false
		}
		return len(supported) > 0
	}
}

// allPeersFilter is used to accept all peers in a given subnet
func allPeersFilter(id peer.ID) bool {
	return true
}
