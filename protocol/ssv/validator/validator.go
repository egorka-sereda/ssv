package validator

import (
	"context"
	"encoding/hex"
	msgqueue2 "github.com/bloxapp/ssv/protocol/ssv/msgqueue"
	"github.com/bloxapp/ssv/protocol/ssv/runner"
	"github.com/bloxapp/ssv/protocol/types"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	specssv "github.com/bloxapp/ssv-spec/ssv"
	spectypes "github.com/bloxapp/ssv-spec/types"
	logging "github.com/ipfs/go-log"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/ibft/storage"
)

var logger = logging.Logger("ssv/protocol/ssv/validator").Desugar()

// Validator represents an SSV ETH consensus validator Share assigned, coordinates duty execution and more.
// Every validator has a validatorID which is validator's public key.
// Each validator has multiple DutyRunners, for each duty type.
type Validator struct {
	ctx    context.Context
	cancel context.CancelFunc
	logger *zap.Logger

	DutyRunners runner.DutyRunners
	Network     specqbft.Network
	Beacon      specssv.BeaconNode
	Share       *types.SSVShare
	Signer      spectypes.KeyManager

	Storage *storage.QBFTStores
	Queues  map[spectypes.BeaconRole]msgqueue2.MsgQueue

	state uint32
}

// NewValidator creates a new instance of Validator.
func NewValidator(pctx context.Context, options Options) *Validator {
	options.defaults()
	ctx, cancel := context.WithCancel(pctx)

	logger := logger.With(zap.String("validator", hex.EncodeToString(options.SSVShare.ValidatorPubKey)))

	queues := make(map[spectypes.BeaconRole]msgqueue2.MsgQueue)
	indexers := msgqueue2.WithIndexers(msgqueue2.SignedMsgIndexer(), msgqueue2.DecidedMsgIndexer(), msgqueue2.SignedPostConsensusMsgIndexer())
	for _, dutyRunner := range options.DutyRunners {
		q, _ := msgqueue2.New(logger, indexers) // TODO: handle error
		queues[dutyRunner.GetBaseRunner().BeaconRoleType] = q
	}

	v := &Validator{
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
		DutyRunners: options.DutyRunners,
		Network:     options.Network,
		Beacon:      options.Beacon,
		Storage:     options.Storage,
		Share:       options.SSVShare,
		Signer:      options.Signer,
		Queues:      queues,
		state:       uint32(NotStarted),
	}

	return v
}

// StartDuty starts a duty for the validator
func (v *Validator) StartDuty(duty *spectypes.Duty) error {
	dutyRunner := v.DutyRunners[duty.Type]
	if dutyRunner == nil {
		return errors.Errorf("duty type %s not supported", duty.Type.String())
	}
	return dutyRunner.StartNewDuty(duty)
}

// ProcessMessage processes Network Message of all types
func (v *Validator) ProcessMessage(msg *spectypes.SSVMessage) error {
	dutyRunner := v.DutyRunners.DutyRunnerForMsgID(msg.GetID())
	if dutyRunner == nil {
		return errors.Errorf("could not get duty runner for msg ID")
	}

	if err := validateMessage(v.Share.Share, msg); err != nil {
		return errors.Wrap(err, "Message invalid")
	}

	switch msg.GetType() {
	case spectypes.SSVConsensusMsgType:
		signedMsg := &specqbft.SignedMessage{}
		if err := signedMsg.Decode(msg.GetData()); err != nil {
			return errors.Wrap(err, "could not get consensus Message from network Message")
		}
		return dutyRunner.ProcessConsensus(signedMsg)
	case spectypes.SSVPartialSignatureMsgType:
		signedMsg := &specssv.SignedPartialSignatureMessage{}
		if err := signedMsg.Decode(msg.GetData()); err != nil {
			return errors.Wrap(err, "could not get post consensus Message from network Message")
		}

		if signedMsg.Message.Type == specssv.PostConsensusPartialSig {
			return dutyRunner.ProcessPostConsensus(signedMsg)
		}
		return dutyRunner.ProcessPreConsensus(signedMsg)
	default:
		return errors.New("unknown msg")
	}
}

func validateMessage(share spectypes.Share, msg *spectypes.SSVMessage) error {
	if !share.ValidatorPubKey.MessageIDBelongs(msg.GetID()) {
		return errors.New("msg ID doesn't match validator ID")
	}

	if len(msg.GetData()) == 0 {
		return errors.New("msg data is invalid")
	}

	return nil
}
