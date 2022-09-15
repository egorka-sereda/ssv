package controller

import (
	"context"
	"fmt"
	"github.com/bloxapp/ssv/protocol/v1/qbft/pipelines"
	"github.com/bloxapp/ssv/protocol/v1/qbft/validation/signedmsg"
	"sync"
	"sync/atomic"
	"time"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	specssv "github.com/bloxapp/ssv-spec/ssv"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	forksprotocol "github.com/bloxapp/ssv/protocol/forks"
	beaconprotocol "github.com/bloxapp/ssv/protocol/v1/blockchain/beacon"
	"github.com/bloxapp/ssv/protocol/v1/message"
	p2pprotocol "github.com/bloxapp/ssv/protocol/v1/p2p"
	"github.com/bloxapp/ssv/protocol/v1/qbft"
	"github.com/bloxapp/ssv/protocol/v1/qbft/controller/forks"
	forksfactory "github.com/bloxapp/ssv/protocol/v1/qbft/controller/forks/factory"
	"github.com/bloxapp/ssv/protocol/v1/qbft/instance"
	"github.com/bloxapp/ssv/protocol/v1/qbft/msgqueue"
	qbftstorage "github.com/bloxapp/ssv/protocol/v1/qbft/storage"
	"github.com/bloxapp/ssv/protocol/v1/qbft/strategy"
	"github.com/bloxapp/ssv/protocol/v1/qbft/strategy/factory"
)

// ErrAlreadyRunning is used to express that some process is already running, e.g. sync
var ErrAlreadyRunning = errors.New("already running")

// NewDecidedHandler handles newly saved decided messages.
// it will be called in a new goroutine to avoid concurrency issues
type NewDecidedHandler func(msg *specqbft.SignedMessage)

// Options is a set of options for the controller
type Options struct {
	Context           context.Context
	Role              spectypes.BeaconRole
	Identifier        []byte
	Logger            *zap.Logger
	Storage           qbftstorage.QBFTStore
	Network           p2pprotocol.Network
	InstanceConfig    *qbft.InstanceConfig
	ValidatorShare    *beaconprotocol.Share
	Version           forksprotocol.ForkVersion
	Beacon            beaconprotocol.Beacon
	KeyManager        spectypes.KeyManager
	SyncRateLimit     time.Duration
	SigTimeout        time.Duration
	MinPeers          int
	ReadMode          bool
	FullNode          bool
	NewDecidedHandler NewDecidedHandler
}

// set of states for the controller
const (
	NotStarted uint32 = iota
	InitiatedHandlers
	SyncedChangeRound
	WaitingForPeers
	FoundPeers
	Ready
	Forking
)

var stateStringMap = map[uint32]string{
	NotStarted:        "notStarted",
	InitiatedHandlers: "initiatedHandlers",
	SyncedChangeRound: "syncedChangedRound",
	WaitingForPeers:   "waitingForPeers",
	FoundPeers:        "foundPeers",
	Ready:             "ready",
	Forking:           "forking",
}

// Controller implements IController interface
type Controller struct {
	Ctx context.Context

	currentInstance        instance.Instancer
	Logger                 *zap.Logger
	InstanceStorage        qbftstorage.InstanceStore
	ChangeRoundStorage     qbftstorage.ChangeRoundStore
	Network                p2pprotocol.Network
	InstanceConfig         *qbft.InstanceConfig
	ValidatorShare         *beaconprotocol.Share
	Identifier             []byte
	Fork                   forks.Fork
	Beacon                 beaconprotocol.Beacon
	KeyManager             spectypes.KeyManager
	HigherReceivedMessages *specqbft.MsgContainer

	// lockers
	CurrentInstanceLock *sync.RWMutex // not locker interface in order to avoid casting to RWMutex
	ForkLock            sync.Locker

	// signature
	SignatureState SignatureState

	// config
	SyncRateLimit time.Duration
	MinPeers      int

	// state
	State  uint32
	height atomic.Value // specqbft.Height

	// flags
	ReadMode bool
	fullNode bool

	Q msgqueue.MsgQueue

	DecidedFactory    *factory.Factory
	DecidedStrategy   strategy.Decided
	newDecidedHandler NewDecidedHandler

	highestRoundCtxCancel context.CancelFunc
}

// New is the constructor of Controller
func New(opts Options) IController {
	logger := opts.Logger.With(zap.String("role", opts.Role.String()), zap.Bool("read mode", opts.ReadMode))
	fork := forksfactory.NewFork(opts.Version)

	ctrl := &Controller{
		Ctx:                    opts.Context,
		InstanceStorage:        opts.Storage,
		ChangeRoundStorage:     opts.Storage,
		Logger:                 logger,
		Network:                opts.Network,
		InstanceConfig:         opts.InstanceConfig,
		ValidatorShare:         opts.ValidatorShare,
		Identifier:             opts.Identifier,
		Fork:                   fork,
		Beacon:                 opts.Beacon,
		KeyManager:             opts.KeyManager,
		SignatureState:         SignatureState{SignatureCollectionTimeout: opts.SigTimeout},
		HigherReceivedMessages: specqbft.NewMsgContainer(),

		SyncRateLimit: opts.SyncRateLimit,
		MinPeers:      opts.MinPeers,

		ReadMode: opts.ReadMode,
		fullNode: opts.FullNode,

		CurrentInstanceLock: &sync.RWMutex{},
		ForkLock:            &sync.Mutex{},

		newDecidedHandler: opts.NewDecidedHandler,
	}

	if !opts.ReadMode {
		q, err := msgqueue.New(
			logger.With(zap.String("who", "msg_q")),
			msgqueue.WithIndexers( /*msgqueue.DefaultMsgIndexer(), */ msgqueue.SignedMsgIndexer(), msgqueue.DecidedMsgIndexer(), msgqueue.SignedPostConsensusMsgIndexer()),
		)
		if err != nil {
			// TODO: we should probably stop here, TBD
			logger.Warn("could not setup msg queue properly", zap.Error(err))
		}
		ctrl.Q = q
	}

	ctrl.DecidedFactory = factory.NewDecidedFactory(logger, ctrl.GetNodeMode(), opts.Storage, opts.Network)
	ctrl.DecidedStrategy = ctrl.DecidedFactory.GetStrategy()

	// set flags
	ctrl.State = NotStarted
	return ctrl
}

// Init sets all major processes of iBFT while blocking until completed.
// if init fails to sync
func (c *Controller) Init() error {
	// checks if notStarted. if so, preform init handlers and set state to new state
	if atomic.CompareAndSwapUint32(&c.State, NotStarted, InitiatedHandlers) {
		c.Logger.Info("start qbft ctrl handler init")

		go c.StartQueueConsumer(c.MessageHandler)
		c.setInitialHeight()
		ReportIBFTStatus(c.ValidatorShare.PublicKey.SerializeToHexStr(), false, false)
		//c.logger.Debug("managed to setup iBFT handlers")
	}

	// checks if InitiatedHandlers. if so, load change round to queue and then set state to new SyncedChangeRound
	if atomic.CompareAndSwapUint32(&c.State, InitiatedHandlers, SyncedChangeRound) {
		c.loadLastChangeRound()
	}

	// if already waiting for peers no need to redundant waiting
	if atomic.LoadUint32(&c.State) == WaitingForPeers {
		return ErrAlreadyRunning
	}

	// only if finished with handlers, start waiting for peers and syncing
	if atomic.CompareAndSwapUint32(&c.State, SyncedChangeRound, WaitingForPeers) {
		// warmup to avoid network errors
		time.Sleep(500 * time.Millisecond)
		c.Logger.Debug("waiting for min peers...", zap.Int("min peers", c.MinPeers))
		if err := p2pprotocol.WaitForMinPeers(c.Ctx, c.Logger, c.Network, c.ValidatorShare.PublicKey.Serialize(), c.MinPeers, time.Millisecond*500); err != nil {
			atomic.StoreUint32(&c.State, SyncedChangeRound) // rollback state in order to find peers & try syncing again
			return err
		}
		c.Logger.Debug("found enough peers")

		atomic.StoreUint32(&c.State, FoundPeers)

		// IBFT sync to make sure the operator is aligned for this validator
		knownMsg, err := c.DecidedStrategy.GetLastDecided(c.Identifier)
		if err != nil {
			c.Logger.Error("failed to get last known", zap.Error(err))
		}
		if err := c.syncDecided(knownMsg, nil); err != nil {
			if err == ErrAlreadyRunning {
				// don't fail if init is already running
				c.Logger.Debug("iBFT init is already running (syncing history)")
				return nil
			}
			c.Logger.Warn("iBFT implementation init failed to sync history", zap.Error(err))
			ReportIBFTStatus(c.ValidatorShare.PublicKey.SerializeToHexStr(), false, true)
			atomic.StoreUint32(&c.State, SyncedChangeRound) // rollback state in order to find peers & try syncing again
			return errors.Wrap(err, "could not sync history")
		}

		atomic.StoreUint32(&c.State, Ready)

		ReportIBFTStatus(c.ValidatorShare.PublicKey.SerializeToHexStr(), true, false)
		c.Logger.Info("iBFT implementation init finished", zap.Int64("height", int64(c.GetHeight())))
	}

	return nil
}

// initialized return true is done the init process and not in forking state
func (c *Controller) initialized() (bool, error) {
	state := atomic.LoadUint32(&c.State)
	switch state {
	case Ready:
		return true, nil
	case Forking:
		return false, errors.New("forking in progress")
	default:
		return false, errors.Errorf("controller hasn't initialized yet. current state - %s", stateStringMap[state])
	}
}

// StartInstance - starts an ibft instance or returns error
func (c *Controller) StartInstance(opts instance.ControllerStartInstanceOptions, getInstance func(instance instance.Instancer)) (res *instance.Result, err error) {
	instanceOpts, err := c.instanceOptionsFromStartOptions(opts)
	if err != nil {
		return nil, errors.WithMessage(err, "can't generate instance options")
	}

	if err := c.canStartNewInstance(*instanceOpts); err != nil {
		return nil, errors.WithMessage(err, "can't start new iBFT instance")
	}

	done := reportIBFTInstanceStart(c.ValidatorShare.PublicKey.SerializeToHexStr())

	c.setHeight(opts.Height)                                                                    // update once height determent
	instanceOpts.ChangeRoundStore = c.ChangeRoundStorage                                        // in order to set the last change round msg
	if _, err := instanceOpts.ChangeRoundStore.CleanLastChangeRound(c.Identifier); err != nil { // clean previews last change round msg's (TODO place in instance?)
		c.Logger.Warn("could not clean change round", zap.Error(err))
	}

	res, err = c.startInstanceWithOptions(instanceOpts, opts.Value, getInstance)
	defer func() {
		done()
		// report error status if the instance returned error
		if err != nil {
			ReportIBFTStatus(c.ValidatorShare.PublicKey.SerializeToHexStr(), true, true)
			return
		}
	}()

	return res, err
}

// GetCurrentInstance returns current instance if exist. if not, returns nil
func (c *Controller) GetCurrentInstance() instance.Instancer {
	c.CurrentInstanceLock.RLock()
	defer c.CurrentInstanceLock.RUnlock()

	return c.currentInstance
}

// SetCurrentInstance set the current instance
func (c *Controller) SetCurrentInstance(instance instance.Instancer) {
	c.CurrentInstanceLock.Lock()
	defer c.CurrentInstanceLock.Unlock()

	c.currentInstance = instance
}

// OnFork called upon fork, it will make sure all decided messages were processed
// before clearing the entire msg queue.
// it also recreates the fork instance and decided strategy with the new fork version
func (c *Controller) OnFork(forkVersion forksprotocol.ForkVersion) error {
	atomic.StoreUint32(&c.State, Forking)
	defer atomic.StoreUint32(&c.State, Ready)

	if i := c.GetCurrentInstance(); i != nil {
		i.Stop()
		c.SetCurrentInstance(nil)
	}
	c.processAllDecided(c.MessageHandler)
	cleared := c.Q.Clean(msgqueue.AllIndicesCleaner)
	c.Logger.Debug("FORKING qbft controller", zap.Int64("clearedMessages", cleared))

	// get new QBFT controller fork and update decidedStrategy
	c.ForkLock.Lock()
	defer c.ForkLock.Unlock()
	c.Fork = forksfactory.NewFork(forkVersion)
	c.DecidedStrategy = c.DecidedFactory.GetStrategy()
	return nil
}

func (c *Controller) syncDecided(from, to *specqbft.SignedMessage) error {
	c.ForkLock.Lock()
	decidedStrategy := c.DecidedStrategy
	c.ForkLock.Unlock()
	msgs, err := decidedStrategy.Sync(c.Ctx, c.Identifier, from, to)
	if err != nil {
		return err
	}
	return c.handleSyncMessages(msgs)
}

func (c *Controller) handleSyncMessages(msgs []*specqbft.SignedMessage) error {
	c.Logger.Debug(fmt.Sprintf("recivied %d msgs from sync", len(msgs)))
	for _, syncMsg := range msgs {
		if err := pipelines.Combine( // TODO need to move it into sync?
			signedmsg.BasicMsgValidation(),
			signedmsg.ValidateIdentifiers(c.Identifier)).Run(syncMsg); err != nil {
			c.Logger.Warn("invalid sync msg", zap.Error(err))
			continue
		}
		encoded, err := syncMsg.Encode() // TODo move to better place
		if err != nil {
			c.Logger.Warn("failed to encode sync msg", zap.Error(err))
			continue
		}
		if err := c.ProcessMsg(&spectypes.SSVMessage{
			MsgType: spectypes.SSVDecidedMsgType, // sync only for decided msg type
			MsgID:   message.ToMessageID(syncMsg.Message.Identifier),
			Data:    encoded,
		}); err != nil {
			c.Logger.Warn("failed to process sync msg", zap.Error(err))
		}
	}
	return nil
}

// GetIBFTCommittee returns a map of the iBFT committee where the key is the member's id.
func (c *Controller) GetIBFTCommittee() map[spectypes.OperatorID]*beaconprotocol.Node {
	return c.ValidatorShare.Committee
}

// GetIdentifier returns ibft identifier made of public key and role (type)
func (c *Controller) GetIdentifier() []byte {
	return c.Identifier[:] // TODO should use mutex to lock var?
}

// ProcessMsg takes an incoming message, and adds it to the message queue or handle it on read mode
func (c *Controller) ProcessMsg(msg *spectypes.SSVMessage) error {
	if c.ReadMode {
		return c.MessageHandler(msg)
	}
	var fields []zap.Field
	cInstance := c.GetCurrentInstance()
	if cInstance != nil {
		currentState := cInstance.GetState()
		if currentState != nil {
			fields = append(fields, zap.String("instance stage", qbft.RoundStateName[currentState.Stage.Load()]), zap.Uint32("instance height", uint32(currentState.GetHeight())), zap.Uint32("instance round", uint32(currentState.GetRound())))
		}
	}
	fields = append(fields,
		zap.Int("queue_len", c.Q.Len()),
		zap.String("msgType", message.MsgTypeToString(msg.MsgType)),
	)
	c.Logger.Debug("got message, add to queue", fields...)
	c.Q.Add(msg)
	return nil
}

// MessageHandler process message from queue,
func (c *Controller) MessageHandler(msg *spectypes.SSVMessage) error {
	switch msg.GetType() {
	case spectypes.SSVConsensusMsgType, spectypes.SSVDecidedMsgType:
		signedMsg := &specqbft.SignedMessage{}
		if err := signedMsg.Decode(msg.GetData()); err != nil {
			return errors.Wrap(err, "could not get post consensus Message from SSVMessage")
		}
		_, err := c.processConsensusMsg(signedMsg)
		return err

	case spectypes.SSVPartialSignatureMsgType:
		signedMsg := &specssv.SignedPartialSignatureMessage{}
		if err := signedMsg.Decode(msg.GetData()); err != nil {
			return errors.Wrap(err, "could not get post consensus Message from network Message")
		}
		return c.processPostConsensusSig(signedMsg)
	case message.SSVSyncMsgType:
		panic("need to implement!")
	}
	return nil
}

// GetNodeMode return node type
func (c *Controller) GetNodeMode() strategy.Mode {
	isPostFork := c.Fork.VersionName() != forksprotocol.GenesisForkVersion.String()
	if !isPostFork { // by default when pre fork, the mode is fullnode
		return strategy.ModeFullNode
	}
	// otherwise, checking flag
	if c.fullNode {
		return strategy.ModeFullNode
	}
	return strategy.ModeLightNode
}

// setInitialHeight fetch height decided and set height. if not found set first height
func (c *Controller) setInitialHeight() {
	height, err := c.highestKnownDecided()
	if err != nil {
		c.Logger.Error("failed to get next height number", zap.Error(err))
	}
	if height == nil {
		c.setHeight(specqbft.FirstHeight)
		return
	}
	c.setHeight(height.Message.Height) // make sure ctrl is set with the right height
}

// GetHeight return current ctrl height
func (c *Controller) GetHeight() specqbft.Height {
	if height, ok := c.height.Load().(specqbft.Height); ok {
		return height
	}

	return specqbft.Height(0)
}

// setHeight set ctrl current height
func (c *Controller) setHeight(height specqbft.Height) {
	c.Logger.Debug("set new ctrl height", zap.Int64("current height", int64(c.GetHeight())), zap.Int64("new height", int64(height)))
	c.height.Store(height)
}
