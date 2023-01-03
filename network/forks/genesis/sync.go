package genesis

import (
	p2pprotocol "github.com/bloxapp/ssv/protocol/p2p"
	"github.com/libp2p/go-libp2p-core/protocol"
)

const (
	lastDecidedProtocol = "/ssv/sync/decided/last/0.0.1"
	historyProtocol     = "/ssv/sync/decided/history/0.0.1"

	peersForSync = 10
)

// ProtocolID returns the protocol id of the given protocol,
// and the amount of peers for distribution
func (f *ForkGenesis) ProtocolID(prot p2pprotocol.SyncProtocol) (protocol.ID, int) {
	switch prot {
	case p2pprotocol.LastDecidedProtocol:
		return lastDecidedProtocol, peersForSync
	case p2pprotocol.DecidedHistoryProtocol:
		return historyProtocol, peersForSync
	}
	return "", 0
}
