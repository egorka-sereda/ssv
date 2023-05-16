package tests

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	spec "github.com/attestantio/go-eth2-client/spec/phase0"
	spectypes "github.com/bloxapp/ssv-spec/types"
	spectestingutils "github.com/bloxapp/ssv-spec/types/testingutils"
	qbftstorage "github.com/bloxapp/ssv/ibft/storage"
	"github.com/bloxapp/ssv/network"
	"github.com/bloxapp/ssv/operator/duties"
	"github.com/bloxapp/ssv/operator/validator"
	protocolforks "github.com/bloxapp/ssv/protocol/forks"
	protocolbeacon "github.com/bloxapp/ssv/protocol/v2/blockchain/beacon"
	protocolp2p "github.com/bloxapp/ssv/protocol/v2/p2p"
	protocolstorage "github.com/bloxapp/ssv/protocol/v2/qbft/storage"
	"github.com/bloxapp/ssv/protocol/v2/ssv/queue"
	protocolvalidator "github.com/bloxapp/ssv/protocol/v2/ssv/validator"
	"github.com/bloxapp/ssv/protocol/v2/sync/handlers"
	"github.com/bloxapp/ssv/protocol/v2/types"
	"github.com/bloxapp/ssv/storage"
	"github.com/bloxapp/ssv/storage/basedb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var (
	KeySet4Committee  = spectestingutils.Testing4SharesSet()
	KeySet7Committee  = spectestingutils.Testing7SharesSet()
	KeySet10Committee = spectestingutils.Testing10SharesSet()
	KeySet13Committee = spectestingutils.Testing13SharesSet()
)

type Scenario struct {
	Committee           int
	ExpectedHeight      int
	Duties              map[spectypes.OperatorID]DutyProperties
	ValidationFunctions map[spectypes.OperatorID]func(t *testing.T, committee int, actual *protocolstorage.StoredInstance)
	shared              SharedData
	validators          map[spectypes.OperatorID]*protocolvalidator.Validator
}

func (s *Scenario) Run(t *testing.T, role spectypes.BeaconRole) {
	t.Run(role.String(), func(t *testing.T) {
		//preparing resources
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s.validators = map[spectypes.OperatorID]*protocolvalidator.Validator{} //initiating map

		s.shared = GetSharedData(t)

		//initiating validators
		for id := 1; id <= s.Committee; id++ {
			id := spectypes.OperatorID(id)
			s.validators[id] = createValidator(t, ctx, id, getKeySet(s.Committee), s.shared.Logger, s.shared.Nodes[id])

			stores := newStores(s.shared.Logger)
			s.shared.Nodes[id].RegisterHandlers(protocolp2p.WithHandler(
				protocolp2p.LastDecidedProtocol,
				handlers.LastDecidedHandler(s.shared.Logger.Named(fmt.Sprintf("decided-handler-%d", id)), stores, s.shared.Nodes[id]),
			), protocolp2p.WithHandler(
				protocolp2p.DecidedHistoryProtocol,
				handlers.HistoryHandler(s.shared.Logger.Named(fmt.Sprintf("history-handler-%d", id)), stores, s.shared.Nodes[id], 25),
			))
		}

		//invoking duties
		for id, dutyProp := range s.Duties {
			go func(id spectypes.OperatorID, dutyProp DutyProperties) { //launching goroutine for every validator
				time.Sleep(dutyProp.Delay)

				duty := createDuty(getKeySet(s.Committee).ValidatorPK.Serialize(), dutyProp.Slot, dutyProp.ValidatorIndex, role)

				ssvMsg, err := duties.CreateDutyExecuteMsg(duty, getKeySet(s.Committee).ValidatorPK)
				require.NoError(t, err)
				dec, err := queue.DecodeSSVMessage(ssvMsg)
				require.NoError(t, err)

				s.validators[id].Queues[role].Q.Push(dec)
			}(id, dutyProp)
		}

		//validating state of validator after invoking duties
		for id, validationFunc := range s.ValidationFunctions {
			identifier := spectypes.NewMsgID(getKeySet(s.Committee).ValidatorPK.Serialize(), role)
			//getting stored state of validator
			var storedInstance *protocolstorage.StoredInstance
			for {
				var err error
				storedInstance, err = s.validators[id].Storage.Get(spectypes.MessageIDFromBytes(identifier[:]).GetRoleType()).GetHighestInstance(identifier[:])
				require.NoError(t, err)

				if storedInstance != nil {
					break
				}

				time.Sleep(500 * time.Millisecond) // waiting for duty will be done and storedInstance would be saved
			}

			//validating stored state of validator
			validationFunc(t, s.Committee, storedInstance)
		}

		// teardown
		for _, val := range s.validators {
			require.NoError(t, val.Stop())
		}

		// HACK: sleep to wait for function calls to github.com/herumi/bls-eth-go-binary
		// to return. When val.Stop() is called, the context.Context that controls the procedure to
		// pop & process messages from by the validator from its queue will stop running new iterations.
		// But if a procedure to pop & process a message is in progress when val.Stop() is called, the
		// popped message will still be processed. When a message is processed the  github.com/herumi/bls-eth-go-binary
		// library is used. When this test function returns, the validator and all of its resources are
		// garbage collected by the Go test runtime. Because this library is a cgo wrapper of a C/C++ library,
		// the C/C++ code will continue to try to access the signature data of the message even though it has been garbage
		// collected already. This causes the C code to receive a SIGSEGV (SIGnal SEGmentation Violation) which
		// crashes the Go runtime in a way that is not recoverable. A long term fix would involve signaling
		// when the validator ConsumeQueue() function has returned, as its processing is synchronous.
		time.Sleep(time.Millisecond * 500)
	})
}

// getKeySet returns the keyset for a given committee size. Some tests have a
// committee size smaller than 3f+1 in order to simulate cases where operators are offline
func getKeySet(committee int) *spectestingutils.TestKeySet {
	switch committee {
	case 1, 2, 3, 4:
		return KeySet4Committee
	case 5, 6, 7:
		return KeySet7Committee
	case 8, 9, 10:
		return KeySet10Committee
	case 11, 12, 13:
		return KeySet13Committee
	default:
		panic("unsupported committee size")

	}
}

func testingShare(keySet *spectestingutils.TestKeySet, id spectypes.OperatorID) *spectypes.Share { //TODO: check dead-locks
	return &spectypes.Share{
		OperatorID:      id,
		ValidatorPubKey: keySet.ValidatorPK.Serialize(),
		SharePubKey:     keySet.Shares[id].GetPublicKey().Serialize(),
		DomainType:      spectypes.PrimusTestnet,
		Quorum:          keySet.Threshold,
		PartialQuorum:   keySet.PartialThreshold,
		Committee:       keySet.Committee(),
	}
}

func quorum(committee int) int {
	return (committee*2 + 1) / 3 // committee = 3f+1; quorum = 2f+1
}

func newStores(logger *zap.Logger) *qbftstorage.QBFTStores {
	db, err := storage.GetStorageFactory(basedb.Options{
		Type:   "badger-memory",
		Path:   "",
		Logger: logger,
	})
	if err != nil {
		panic(err)
	}

	storageMap := qbftstorage.NewStores()

	roles := []spectypes.BeaconRole{
		spectypes.BNRoleAttester,
		spectypes.BNRoleProposer,
		spectypes.BNRoleAggregator,
		spectypes.BNRoleSyncCommittee,
		spectypes.BNRoleSyncCommitteeContribution,
	}
	for _, role := range roles {
		storageMap.Add(role, qbftstorage.New(db, logger, role.String(), protocolforks.GenesisForkVersion))
	}

	return storageMap
}

func createValidator(t *testing.T, pCtx context.Context, id spectypes.OperatorID, keySet *spectestingutils.TestKeySet, pLogger *zap.Logger, node network.P2PNetwork) *protocolvalidator.Validator {
	ctx, cancel := context.WithCancel(pCtx)
	validatorPubKey := keySet.Shares[id].GetPublicKey().Serialize()
	logger := pLogger.With(zap.Int("operator-id", int(id)), zap.String("validator", hex.EncodeToString(validatorPubKey)))
	km := spectestingutils.NewTestingKeyManager()
	err := km.AddShare(keySet.Shares[id])
	require.NoError(t, err)

	options := protocolvalidator.Options{
		Storage: newStores(logger),
		Network: node,
		SSVShare: &types.SSVShare{
			Share: *testingShare(keySet, id),
			Metadata: types.Metadata{
				BeaconMetadata: &protocolbeacon.ValidatorMetadata{
					Index: spec.ValidatorIndex(1),
				},
				OwnerAddress: "0x0",
				Liquidated:   false,
			},
		},
		Beacon: spectestingutils.NewTestingBeaconNode(),
		Signer: km,
	}

	options.DutyRunners = validator.SetupRunners(ctx, logger, options)
	val := protocolvalidator.NewValidator(ctx, cancel, options)
	node.UseMessageRouter(newMsgRouter(val))
	require.NoError(t, val.Start())

	return val
}
