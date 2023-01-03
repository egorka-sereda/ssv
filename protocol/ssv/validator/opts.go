package validator

import (
	specqbft "github.com/bloxapp/ssv-spec/qbft"
	specssv "github.com/bloxapp/ssv-spec/ssv"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/bloxapp/ssv/ibft/storage"
	"github.com/bloxapp/ssv/protocol/ssv/runner"
	"github.com/bloxapp/ssv/protocol/types"
)

// Options represents options that should be passed to a new instance of Validator.
type Options struct {
	Network     specqbft.Network
	Beacon      specssv.BeaconNode
	Storage     *storage.QBFTStores
	SSVShare    *types.SSVShare
	Signer      spectypes.KeyManager
	DutyRunners runner.DutyRunners
	FullNode    bool
}

func (o *Options) defaults() {
	// Nothing to set yet.
}

// State of the validator
type State uint32

const (
	// NotStarted the validator hasn't started
	NotStarted State = iota
	// Started validator is running
	Started
)
