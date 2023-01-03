package duties

import (
	"context"
	"encoding/hex"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/bloxapp/ssv/protocol/blockchain/beacon"
	"time"

	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/herumi/bls-eth-go-binary/bls"
	"github.com/pkg/errors"
	types "github.com/prysmaticlabs/eth2-types"
	"github.com/prysmaticlabs/prysm/time/slots"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/operator/validator"
	forksprotocol "github.com/bloxapp/ssv/protocol/forks"
)

//go:generate mockgen -package=mocks -destination=./mocks/controller.go -source=./controller.go

const (
	slotChanBuffer = 32
)

// DutyExecutor represents the component that executes duties
type DutyExecutor interface {
	ExecuteDuty(duty *spectypes.Duty) error
}

// DutyController interface for dispatching duties execution according to slot ticker
type DutyController interface {
	Start()
	// CurrentSlotChan will trigger every slot
	CurrentSlotChan() <-chan uint64
}

// ControllerOptions holds the needed dependencies
type ControllerOptions struct {
	Logger              *zap.Logger
	Ctx                 context.Context
	BeaconClient        beacon.Beacon
	EthNetwork          beacon.Network
	ValidatorController validator.Controller
	Executor            DutyExecutor
	GenesisEpoch        uint64
	DutyLimit           uint64
	ForkVersion         forksprotocol.ForkVersion
}

// dutyController internal implementation of DutyController
type dutyController struct {
	logger     *zap.Logger
	ctx        context.Context
	ethNetwork beacon.Network
	// executor enables to work with a custom execution
	executor            DutyExecutor
	fetcher             DutyFetcher
	validatorController validator.Controller
	genesisEpoch        uint64
	dutyLimit           uint64

	// chan
	currentSlotC chan uint64
}

var secPerSlot int64 = 12

// NewDutyController creates a new instance of DutyController
func NewDutyController(opts *ControllerOptions) DutyController {
	fetcher := newDutyFetcher(opts.Logger, opts.BeaconClient, opts.ValidatorController, opts.EthNetwork)
	dc := dutyController{
		logger:              opts.Logger,
		ctx:                 opts.Ctx,
		ethNetwork:          opts.EthNetwork,
		fetcher:             fetcher,
		validatorController: opts.ValidatorController,
		genesisEpoch:        opts.GenesisEpoch,
		dutyLimit:           opts.DutyLimit,
		executor:            opts.Executor,
	}
	return &dc
}

// Start listens to slot ticker and dispatches duties execution
func (dc *dutyController) Start() {
	// warmup
	indices := dc.validatorController.GetValidatorsIndices()
	dc.logger.Debug("warming up indices", zap.Int("count", len(indices)))

	genesisTime := time.Unix(int64(dc.ethNetwork.MinGenesisTime()), 0)
	slotTicker := slots.NewSlotTicker(genesisTime, uint64(dc.ethNetwork.SlotDurationSec().Seconds()))
	dc.listenToTicker(slotTicker.C())
}

func (dc *dutyController) CurrentSlotChan() <-chan uint64 {
	if dc.currentSlotC == nil {
		dc.currentSlotC = make(chan uint64, slotChanBuffer)
	}
	return dc.currentSlotC
}

// ExecuteDuty tries to execute the given duty
func (dc *dutyController) ExecuteDuty(duty *spectypes.Duty) error {
	if dc.executor != nil {
		// enables to work with a custom executor, e.g. readOnlyDutyExec
		return dc.executor.ExecuteDuty(duty)
	}
	logger := dc.loggerWithDutyContext(dc.logger, duty)

	// because we're using the same duty for more than 1 duty (e.g. attest + aggregator) there is an error in bls.Deserialize func for cgo pointer to pointer.
	// so we need to copy the pubkey val to avoid pointer
	var pk phase0.BLSPubKey
	copy(pk[:], duty.PubKey[:])

	pubKey := &bls.PublicKey{}
	if err := pubKey.Deserialize(pk[:]); err != nil {
		return errors.Wrap(err, "failed to deserialize pubkey from duty")
	}
	if v, ok := dc.validatorController.GetValidator(pubKey.SerializeToHexStr()); ok {
		go func() {
			// force the validator to be started (subscribed to validator's topic and synced)
			// TODO: handle error (return error
			if err := v.Start(); err != nil {
				logger.Warn("could not start validator", zap.Error(err))
				return
			}
			logger.Info("starting duty processing")
			if err := v.StartDuty(duty); err != nil {
				logger.Warn("could not start duty", zap.Error(err))
			}
		}()
	} else {
		logger.Warn("could not find validator")
	}
	return nil
}

// listenToTicker loop over the given slot channel
func (dc *dutyController) listenToTicker(slots <-chan types.Slot) {
	for currentSlot := range slots {
		// notify current slot to channel
		go dc.notifyCurrentSlot(currentSlot)

		// execute duties
		dc.logger.Info("slot ticker", zap.Uint64("slot", uint64(currentSlot)))
		duties, err := dc.fetcher.GetDuties(uint64(currentSlot))
		if err != nil {
			dc.logger.Warn("failed to get duties", zap.Error(err))
		}
		for i := range duties {
			go dc.onDuty(&duties[i])
		}
	}
}

func (dc *dutyController) notifyCurrentSlot(slot types.Slot) {
	if dc.currentSlotC != nil {
		dc.currentSlotC <- uint64(slot)
	}
}

// onDuty handles next duty
func (dc *dutyController) onDuty(duty *spectypes.Duty) {
	logger := dc.loggerWithDutyContext(dc.logger, duty)
	if dc.shouldExecute(duty) {
		logger.Debug("duty was sent to execution")
		if err := dc.ExecuteDuty(duty); err != nil {
			logger.Warn("could not dispatch duty", zap.Error(err))
			return
		}
		return
	}
	logger.Warn("slot is irrelevant, ignoring duty")
}

func (dc *dutyController) shouldExecute(duty *spectypes.Duty) bool {
	if uint64(duty.Slot) < dc.getEpochFirstSlot(dc.genesisEpoch) {
		// wait until genesis epoch starts
		dc.logger.Debug("skipping slot, lower than genesis",
			zap.Uint64("genesis_slot", dc.getEpochFirstSlot(dc.genesisEpoch)),
			zap.Uint64("slot", uint64(duty.Slot)))
		return false
	}

	currentSlot := uint64(dc.ethNetwork.EstimatedCurrentSlot())
	// execute task if slot already began and not pass 1 epoch
	if currentSlot >= uint64(duty.Slot) && currentSlot-uint64(duty.Slot) <= dc.dutyLimit {
		return true
	} else if currentSlot+1 == uint64(duty.Slot) {
		dc.loggerWithDutyContext(dc.logger, duty).Debug("current slot and duty slot are not aligned, "+
			"assuming diff caused by a time drift - ignoring and executing duty", zap.String("type", duty.Type.String()))
		return true
	}
	return false
}

// loggerWithDutyContext returns an instance of logger with the given duty's information
func (dc *dutyController) loggerWithDutyContext(logger *zap.Logger, duty *spectypes.Duty) *zap.Logger {
	currentSlot := uint64(dc.ethNetwork.EstimatedCurrentSlot())
	return logger.
		With(zap.String("role", duty.Type.String())).
		With(zap.Uint64("committee_index", uint64(duty.CommitteeIndex))).
		With(zap.Uint64("current slot", currentSlot)).
		With(zap.Uint64("slot", uint64(duty.Slot))).
		With(zap.Uint64("epoch", uint64(duty.Slot)/32)).
		With(zap.String("pubKey", hex.EncodeToString(duty.PubKey[:]))).
		With(zap.Time("start_time", dc.ethNetwork.GetSlotStartTime(uint64(duty.Slot))))
}

// getEpochFirstSlot returns the beacon node first slot in epoch
func (dc *dutyController) getEpochFirstSlot(epoch uint64) uint64 {
	return epoch * 32
}

// NewReadOnlyExecutor creates a dummy executor that is used to run in read mode
func NewReadOnlyExecutor(logger *zap.Logger) DutyExecutor {
	return &readOnlyDutyExec{logger: logger}
}

type readOnlyDutyExec struct {
	logger *zap.Logger
}

func (e *readOnlyDutyExec) ExecuteDuty(duty *spectypes.Duty) error {
	e.logger.Debug("skipping duty execution",
		zap.Uint64("epoch", uint64(duty.Slot)/32),
		zap.Uint64("slot", uint64(duty.Slot)),
		zap.String("pubKey", hex.EncodeToString(duty.PubKey[:])))
	return nil
}
