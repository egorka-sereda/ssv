package network

import (
	"github.com/bloxapp/ssv/protocol/p2p"
	"io"

	spectypes "github.com/bloxapp/ssv-spec/types"
)

// MessageRouter is accepting network messages and route them to the corresponding (internal) components
type MessageRouter interface {
	// Route routes the given message, this function MUST NOT block
	Route(message spectypes.SSVMessage)
}

// MessageRouting allows to register a MessageRouter
type MessageRouting interface {
	// UseMessageRouter registers a message router to handle incoming messages
	UseMessageRouter(router MessageRouter)
}

// P2PNetwork is a facade interface that provides the entire functionality of the different network interfaces
type P2PNetwork interface {
	io.Closer
	protocolp2p.Network
	MessageRouting
	// Setup initialize the network layer and starts the libp2p host
	Setup() error
	// Start starts the network
	Start() error
	// UpdateSubnets will update the registered subnets according to active validators
	UpdateSubnets()
}

// GetValidatorStats returns stats of validators, including the following:
//  - the amount of validators in the network
//  - the amount of active validators in the network (i.e. not slashed or existed)
//  - the amount of validators assigned to this operator
type GetValidatorStats func() (uint64, uint64, uint64, error)
