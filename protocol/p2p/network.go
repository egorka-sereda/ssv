package protocolp2p

import (
	"errors"
	"github.com/bloxapp/ssv/protocol/message"

	"github.com/bloxapp/ssv-spec/p2p"
	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/libp2p/go-libp2p-core/peer"
)

var (
	// ErrNetworkIsNotReady is returned when trying to access the network instance before it's ready
	ErrNetworkIsNotReady = errors.New("network services are not ready")
)

// Subscriber manages topics subscription
type Subscriber interface {
	p2p.Subscriber
	// Unsubscribe unsubscribes from the validator subnet
	Unsubscribe(pk spectypes.ValidatorPK) error
	// Peers returns the peers that are connected to the given validator
	Peers(pk spectypes.ValidatorPK) ([]peer.ID, error)
}

// Broadcaster enables to broadcast messages
type Broadcaster interface {
	p2p.Broadcaster
}

// RequestHandler handles p2p requests
type RequestHandler func(*spectypes.SSVMessage) (*spectypes.SSVMessage, error)

// CombineRequestHandlers combines multiple handlers into a single handler
func CombineRequestHandlers(handlers ...RequestHandler) RequestHandler {
	return func(ssvMessage *spectypes.SSVMessage) (res *spectypes.SSVMessage, err error) {
		for _, handler := range handlers {
			res, err = handler(ssvMessage)
			if err != nil {
				return nil, err
			}
			if res != nil {
				return res, nil
			}
		}
		return
	}
}

// SyncResult holds the result of a sync request, including the actual message and the sender
type SyncResult struct {
	Msg    *spectypes.SSVMessage
	Sender string
}

type SyncResults []SyncResult

func (results SyncResults) ForEachSignedMessage(iterator func(message *specqbft.SignedMessage)) {
	for _, res := range results {
		if res.Msg == nil {
			continue
		}
		sm := &message.SyncMessage{}
		err := sm.Decode(res.Msg.Data)
		if err != nil {
			continue
		}
		for _, m := range sm.Data {
			iterator(m)
		}
	}
}

// SyncProtocol represent the type of sync protocols
type SyncProtocol int32

const (
	// LastDecidedProtocol is the last decided protocol type
	LastDecidedProtocol SyncProtocol = iota
	// DecidedHistoryProtocol is the decided history protocol type
	DecidedHistoryProtocol
)

// SyncHandler is a wrapper for RequestHandler, that enables to specify the protocol
type SyncHandler struct {
	Protocol SyncProtocol
	Handler  RequestHandler
}

// WithHandler enables to inject an SyncHandler
func WithHandler(protocol SyncProtocol, handler RequestHandler) *SyncHandler {
	return &SyncHandler{
		Protocol: protocol,
		Handler:  handler,
	}
}

// Syncer holds the interface for syncing data from other peers
type Syncer interface {
	specqbft.Syncer
	// GetHistory sync the given range from a set of peers that supports history for the given identifier
	// it accepts a list of targets for the request.
	GetHistory(mid spectypes.MessageID, from, to specqbft.Height, targets ...string) ([]SyncResult, specqbft.Height, error)

	// RegisterHandlers registers handler for the given protocol
	RegisterHandlers(handlers ...*SyncHandler)

	// LastDecided fetches last decided from a random set of peers
	// TODO: replace with specqbft.SyncHighestDecided
	LastDecided(mid spectypes.MessageID) ([]SyncResult, error)
}

// MsgValidationResult helps other components to report message validation with a generic results scheme
type MsgValidationResult int32

const (
	// ValidationAccept is the result of a valid message
	ValidationAccept MsgValidationResult = iota
	// ValidationIgnore is the result in case we want to ignore the validation
	ValidationIgnore
	// ValidationRejectLow is the result for invalid message, with low severity (e.g. late message)
	ValidationRejectLow
	// ValidationRejectMedium is the result for invalid message, with medium severity (e.g. wrong height)
	ValidationRejectMedium
	// ValidationRejectHigh is the result for invalid message, with high severity (e.g. invalid signature)
	ValidationRejectHigh
)

// ValidationReporting is the interface for reporting on message validation results
type ValidationReporting interface {
	// ReportValidation reports the result for the given message
	ReportValidation(message *spectypes.SSVMessage, res MsgValidationResult)
}

// Network holds the networking layer used to complement the underlying protocols
type Network interface {
	Subscriber
	Broadcaster
	Syncer
	ValidationReporting
}
