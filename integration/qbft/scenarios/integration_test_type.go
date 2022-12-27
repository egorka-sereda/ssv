package scenarios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	spec "github.com/attestantio/go-eth2-client/spec/phase0"
	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectypes "github.com/bloxapp/ssv-spec/types"
	spectestingutils "github.com/bloxapp/ssv-spec/types/testingutils"
	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	qbftstorage "github.com/bloxapp/ssv/ibft/storage"
	"github.com/bloxapp/ssv/network"
	p2pv1 "github.com/bloxapp/ssv/network/p2p"
	"github.com/bloxapp/ssv/network/testing"
	"github.com/bloxapp/ssv/operator/validator"
	protocolforks "github.com/bloxapp/ssv/protocol/forks"
	protocolbeacon "github.com/bloxapp/ssv/protocol/v2/blockchain/beacon"
	protocolp2p "github.com/bloxapp/ssv/protocol/v2/p2p"
	protocolstorage "github.com/bloxapp/ssv/protocol/v2/qbft/storage"
	protocolvalidator "github.com/bloxapp/ssv/protocol/v2/ssv/validator"
	"github.com/bloxapp/ssv/protocol/v2/sync/handlers"
	"github.com/bloxapp/ssv/protocol/v2/types"
	"github.com/bloxapp/ssv/storage"
	"github.com/bloxapp/ssv/storage/basedb"
	"github.com/bloxapp/ssv/utils/logex"
)

// IntegrationTest defines an integration test.
type IntegrationTest struct {
	Name              string
	OperatorIDs       []spectypes.OperatorID
	InitialInstances  map[spectypes.OperatorID][]*protocolstorage.StoredInstance
	Duties            map[spectypes.OperatorID][]*spectypes.Duty
	ExpectedInstances map[spectypes.OperatorID][]*protocolstorage.StoredInstance
	StartDutyErrors   map[spectypes.OperatorID]error
}

type scenarioContext struct {
	ctx         context.Context
	logger      *zap.Logger
	nodes       map[spectypes.OperatorID]network.P2PNetwork      // 1 per operator, pass same to each instance
	nodeKeys    map[spectypes.OperatorID]testing.NodeKeys        // 1 per operator, pass same to each instance
	stores      map[spectypes.OperatorID]*qbftstorage.QBFTStores // 1 store per operator, pass same store to each instance
	keyManagers map[spectypes.OperatorID]spectypes.KeyManager    // 1 per operator, pass same to each instance
	dbs         map[spectypes.OperatorID]basedb.IDb              // 1 per operator, pass same to each instance
}

func (it *IntegrationTest) bootstrap(ctx context.Context) (*scenarioContext, error) {
	loggerFactory := func(s string) *zap.Logger {
		return logex.Build("simulation", zapcore.DebugLevel, nil).With(zap.String("who", s))
	}
	logger := loggerFactory(fmt.Sprintf("Bootstrap/%s", it.Name))
	logger.Info("creating resources")

	types.SetDefaultDomain(spectypes.PrimusTestnet)

	dbs := make(map[spectypes.OperatorID]basedb.IDb)
	for _, operatorID := range it.OperatorIDs {
		db, err := storage.GetStorageFactory(basedb.Options{
			Type:   "badger-memory",
			Path:   "",
			Logger: zap.L(),
		})
		if err != nil {
			return nil, err
		}

		dbs[operatorID] = db
	}

	forkVersion := protocolforks.GenesisForkVersion

	ln, err := p2pv1.CreateAndStartLocalNet(ctx, loggerFactory, forkVersion, len(it.OperatorIDs), len(it.OperatorIDs)/2, false)
	if err != nil {
		return nil, err
	}

	nodes := make(map[spectypes.OperatorID]network.P2PNetwork)
	nodeKeys := make(map[spectypes.OperatorID]testing.NodeKeys)

	for i, operatorID := range it.OperatorIDs {
		nodes[operatorID] = ln.Nodes[i]
		nodeKeys[operatorID] = ln.NodeKeys[i]
	}

	stores := make(map[spectypes.OperatorID]*qbftstorage.QBFTStores)
	kms := make(map[spectypes.OperatorID]spectypes.KeyManager)
	for _, operatorID := range it.OperatorIDs {
		store := qbftstorage.New(dbs[operatorID], loggerFactory(fmt.Sprintf("qbft-store-%d", operatorID)), "attestations", forkVersion)

		storageMap := qbftstorage.NewStores()
		storageMap.Add(spectypes.BNRoleAttester, store)
		storageMap.Add(spectypes.BNRoleProposer, store)
		storageMap.Add(spectypes.BNRoleAggregator, store)
		storageMap.Add(spectypes.BNRoleSyncCommittee, store)
		storageMap.Add(spectypes.BNRoleSyncCommitteeContribution, store)

		stores[operatorID] = storageMap
		km := spectestingutils.NewTestingKeyManager()
		kms[operatorID] = km
		nodes[operatorID].RegisterHandlers(protocolp2p.WithHandler(
			protocolp2p.LastDecidedProtocol,
			handlers.LastDecidedHandler(loggerFactory(fmt.Sprintf("decided-handler-%d", operatorID)), storageMap, nodes[operatorID]),
		), protocolp2p.WithHandler(
			protocolp2p.DecidedHistoryProtocol,
			handlers.HistoryHandler(loggerFactory(fmt.Sprintf("history-handler-%d", operatorID)), storageMap, nodes[operatorID], 25),
		))
	}

	sCtx := &scenarioContext{
		ctx:         ctx,
		logger:      logger,
		nodes:       nodes,
		nodeKeys:    nodeKeys,
		stores:      stores,
		keyManagers: kms,
		dbs:         dbs,
	}
	return sCtx, nil
}

func (it *IntegrationTest) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sCtx, err := it.bootstrap(ctx)
	if err != nil {
		return err
	}

	validators, err := it.createValidators(sCtx)
	if err != nil {
		return fmt.Errorf("could not create share: %w", err)
	}

	for _, operatorID := range it.OperatorIDs {
		sCtx.nodes[operatorID].UseMessageRouter(newMsgRouter(validators[operatorID]))
	}

	for operatorID, instances := range it.InitialInstances {
		for _, instance := range instances {
			mid := spectypes.MessageIDFromBytes(instance.State.ID)
			if err := sCtx.stores[operatorID].Get(mid.GetRoleType()).SaveHighestInstance(instance); err != nil {
				return err
			}
		}
	}

	var eg errgroup.Group
	for _, val := range validators {
		v := val
		eg.Go(func() error {
			if err := v.Start(); err != nil {
				return fmt.Errorf("could not start validator: %w", err)
			}
			<-time.After(time.Second * 3)
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	actualErrMap := map[spectypes.OperatorID]error{}
	for _, val := range validators {
		for _, duty := range it.Duties[val.Share.OperatorID] {
			startDutyErr := val.StartDuty(duty)
			actualErrMap[val.Share.OperatorID] = startDutyErr
		}
	}

	for operatorID, expectedErr := range it.StartDutyErrors {
		if !errors.Is(actualErrMap[operatorID], expectedErr) {
			return fmt.Errorf("got error different from expected (expected %v): %w", expectedErr, actualErrMap[operatorID])
		}
	}

	<-time.After(2 * time.Second)

	for expectedOperatorID, expectedInstances := range it.ExpectedInstances {
		for _, expectedInstance := range expectedInstances {
			mid := spectypes.MessageIDFromBytes(expectedInstance.State.ID)
			storedInstance, err := sCtx.stores[expectedOperatorID].Get(mid.GetRoleType()).
				GetHighestInstance(expectedInstance.State.ID)
			if err != nil {
				return err
			}

			decidedRoot, err := storedInstance.DecidedMessage.GetRoot()
			if err != nil {
				return fmt.Errorf("stored instance decided message root: %w", err)
			}

			expectedDecidedRoot, err := expectedInstance.DecidedMessage.GetRoot()
			if err != nil {
				return fmt.Errorf("ex[ected instance decided message root: %w", err)
			}

			if !bytes.Equal(decidedRoot, expectedDecidedRoot) {
				return fmt.Errorf("decided message roots are not equal")
			}

			if storedInstance.State == nil && expectedInstance.State == nil {
				return nil
			}

			si, ei := storedInstance.State, expectedInstance.State
			if si == nil && ei != nil || si != nil && ei == nil || !matchedStates(*si, *ei) {
				return fmt.Errorf("states don't match")
			}
		}
	}

	return nil
}

func (it *IntegrationTest) createValidators(sCtx *scenarioContext) (map[spectypes.OperatorID]*protocolvalidator.Validator, error) {
	validators := make(map[spectypes.OperatorID]*protocolvalidator.Validator)

	for _, operatorID := range it.OperatorIDs {
		err := sCtx.keyManagers[operatorID].AddShare(spectestingutils.Testing4SharesSet().Shares[operatorID])
		if err != nil {
			return nil, err
		}

		options := protocolvalidator.Options{
			Storage: sCtx.stores[operatorID],
			Network: sCtx.nodes[operatorID],
			SSVShare: &types.SSVShare{
				Share: *testingShare(spectestingutils.Testing4SharesSet(), operatorID),
				Metadata: types.Metadata{
					BeaconMetadata: &protocolbeacon.ValidatorMetadata{
						Index: spec.ValidatorIndex(1),
					},
					OwnerAddress: common.HexToAddress("0x0"),
					Liquidated:   false,
				},
			},
			Beacon: spectestingutils.NewTestingBeaconNode(),
			Signer: sCtx.keyManagers[operatorID],
		}

		l := sCtx.logger.With(zap.String("w", fmt.Sprintf("node-%d", operatorID)))
		val := protocolvalidator.NewValidator(sCtx.ctx, options)
		val.DutyRunners = validator.SetupRunners(sCtx.ctx, l, options)
		validators[operatorID] = val
	}

	return validators, nil
}

var testingShare = func(keysSet *spectestingutils.TestKeySet, id spectypes.OperatorID) *spectypes.Share {
	return &spectypes.Share{
		OperatorID:      id,
		ValidatorPubKey: keysSet.ValidatorPK.Serialize(),
		SharePubKey:     keysSet.Shares[id].GetPublicKey().Serialize(),
		DomainType:      spectypes.PrimusTestnet,
		Quorum:          keysSet.Threshold,
		PartialQuorum:   keysSet.PartialThreshold,
		Committee:       keysSet.Committee(),
	}
}

func createDuties(pk []byte, slot spec.Slot, idx spec.ValidatorIndex, roles ...spectypes.BeaconRole) []*spectypes.Duty {
	var pkBytes [48]byte
	copy(pkBytes[:], pk)

	duties := make([]*spectypes.Duty, 0, len(roles))
	for _, role := range roles {
		var testingDuty *spectypes.Duty
		switch role {
		case spectypes.BNRoleAttester:
			testingDuty = spectestingutils.TestingAttesterDuty
		case spectypes.BNRoleAggregator:
			testingDuty = spectestingutils.TestingAggregatorDuty
		case spectypes.BNRoleProposer:
			testingDuty = spectestingutils.TestingProposerDuty
		case spectypes.BNRoleSyncCommittee:
			testingDuty = spectestingutils.TestingSyncCommitteeDuty
		case spectypes.BNRoleSyncCommitteeContribution:
			testingDuty = spectestingutils.TestingSyncCommitteeContributionDuty
		}

		duties = append(duties, &spectypes.Duty{
			Type:                    role,
			PubKey:                  pkBytes,
			Slot:                    slot,
			ValidatorIndex:          idx,
			CommitteeIndex:          testingDuty.CommitteeIndex,
			CommitteesAtSlot:        testingDuty.CommitteesAtSlot,
			CommitteeLength:         testingDuty.CommitteeLength,
			ValidatorCommitteeIndex: testingDuty.ValidatorCommitteeIndex,
		})
	}

	return duties
}

// pass states by value to modify them
func matchedStates(actual specqbft.State, expected specqbft.State) bool {
	// Since the signers are not deterministic, we need to do a simple assertion instead of checking the root of whole state.
	if expected.Decided {
		for round, messages := range expected.CommitContainer.Msgs {
			signers, _ := actual.CommitContainer.LongestUniqueSignersForRoundAndValue(round, messages[0].Message.Data)
			if !actual.Share.HasQuorum(len(signers)) {
				return false
			}
		}

		actual.CommitContainer = nil
		expected.CommitContainer = nil
	}

	for _, messages := range actual.PrepareContainer.Msgs {
		sort.Slice(messages, func(i, j int) bool {
			return messages[i].Signers[0] < messages[j].Signers[0]
		})
	}

	actualRoot, err := actual.GetRoot()
	if err != nil {
		return false
	}

	expectedRoot, err := expected.GetRoot()
	if err != nil {
		return false
	}

	return bytes.Equal(actualRoot, expectedRoot)
}

type msgRouter struct {
	validator *protocolvalidator.Validator
}

func (m *msgRouter) Route(message spectypes.SSVMessage) {
	m.validator.HandleMessage(&message)
}

func newMsgRouter(v *protocolvalidator.Validator) *msgRouter {
	return &msgRouter{
		validator: v,
	}
}
