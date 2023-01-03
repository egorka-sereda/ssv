package ekm

import (
	"github.com/bloxapp/ssv/protocol/blockchain/beacon"
	"github.com/bloxapp/ssv/protocol/types"
	"testing"

	spec "github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/bloxapp/eth2-key-manager/core"
	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/herumi/bls-eth-go-binary/bls"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/bloxapp/ssv/utils/logex"
	"github.com/bloxapp/ssv/utils/threshold"
)

const (
	sk1Str = "3548db63ab5701878daf25fa877638dc7809778815b9d9ecd5369da33ca9e64f"
	pk1Str = "a8cb269bd7741740cfe90de2f8db6ea35a9da443385155da0fa2f621ba80e5ac14b5c8f65d23fd9ccc170cc85f29e27d"
	sk2Str = "66dd37ae71b35c81022cdde98370e881cff896b689fa9136917f45afce43fd3b"
	pk2Str = "8796fafa576051372030a75c41caafea149e4368aebaca21c9f90d9974b3973d5cee7d7874e4ec9ec59fb2c8945b3e01"
)

func testKeyManager(t *testing.T) spectypes.KeyManager {
	threshold.Init()

	km, err := NewETHKeyManagerSigner(getStorage(t), nil, beacon.NewNetwork(core.PraterNetwork, 0), types.GetDefaultDomain())
	km.(*ethKeyManagerSigner).signingUtils = beacon.NewBeaconMock()
	require.NoError(t, err)

	sk1 := &bls.SecretKey{}
	require.NoError(t, sk1.SetHexString(sk1Str))

	sk2 := &bls.SecretKey{}
	require.NoError(t, sk2.SetHexString(sk2Str))

	require.NoError(t, km.AddShare(sk1))
	require.NoError(t, km.AddShare(sk2))

	return km
}

func TestSignAttestation(t *testing.T) {
	km := testKeyManager(t)

	sk1 := &bls.SecretKey{}
	require.NoError(t, sk1.SetHexString(sk1Str))
	require.NoError(t, km.AddShare(sk1))

	duty := &spectypes.Duty{
		Type:                    spectypes.BNRoleAttester,
		PubKey:                  [48]byte{},
		Slot:                    30,
		ValidatorIndex:          1,
		CommitteeIndex:          2,
		CommitteeLength:         128,
		CommitteesAtSlot:        4,
		ValidatorCommitteeIndex: 3,
	}
	attestationData := &spec.AttestationData{
		Slot:            30,
		Index:           1,
		BeaconBlockRoot: [32]byte{1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2},
		Source: &spec.Checkpoint{
			Epoch: 1,
			Root:  [32]byte{},
		},
		Target: &spec.Checkpoint{
			Epoch: 3,
			Root:  [32]byte{},
		},
	}

	t.Run("sign once", func(t *testing.T) {
		_, sig, err := km.(*ethKeyManagerSigner).SignAttestation(attestationData, duty, sk1.GetPublicKey().Serialize())
		require.NoError(t, err)
		require.NotNil(t, sig)
	})
	t.Run("slashable sign, fail", func(t *testing.T) {
		attestationData.BeaconBlockRoot = [32]byte{2, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2, 3, 4, 5, 6, 1, 2}
		_, sig, err := km.(*ethKeyManagerSigner).SignAttestation(attestationData, duty, sk1.GetPublicKey().Serialize())
		require.EqualError(t, err, "could not sign attestation: slashable attestation (HighestAttestationVote), not signing")
		require.Nil(t, sig)
	})
}

func TestSignRoot(t *testing.T) {
	logex.Build("", zapcore.DebugLevel, &logex.EncodingConfig{})

	require.NoError(t, bls.Init(bls.BLS12_381))

	km := testKeyManager(t)

	t.Run("pk 1", func(t *testing.T) {
		pk := &bls.PublicKey{}
		require.NoError(t, pk.Deserialize(_byteArray(pk1Str)))

		commitData, err := (&specqbft.CommitData{Data: []byte("value1")}).Encode()
		require.NoError(t, err)

		msg := &specqbft.Message{
			MsgType:    specqbft.CommitMsgType,
			Height:     specqbft.Height(3),
			Round:      specqbft.Round(2),
			Identifier: []byte("identifier1"),
			Data:       commitData,
		}

		// sign
		sig, err := km.SignRoot(msg, spectypes.QBFTSignatureType, pk.Serialize())
		require.NoError(t, err)

		// verify
		signed := &specqbft.SignedMessage{
			Signature: sig,
			Signers:   []spectypes.OperatorID{1},
			Message:   msg,
		}

		err = signed.GetSignature().VerifyByOperators(signed, types.GetDefaultDomain(), spectypes.QBFTSignatureType, []*spectypes.Operator{{OperatorID: spectypes.OperatorID(1), PubKey: pk.Serialize()}})
		// res, err := signed.VerifySig(pk)
		require.NoError(t, err)
		// require.True(t, res)
	})

	t.Run("pk 2", func(t *testing.T) {
		pk := &bls.PublicKey{}
		require.NoError(t, pk.Deserialize(_byteArray(pk2Str)))

		commitData, err := (&specqbft.CommitData{Data: []byte("value2")}).Encode()
		require.NoError(t, err)

		msg := &specqbft.Message{
			MsgType:    specqbft.CommitMsgType,
			Height:     specqbft.Height(1),
			Round:      specqbft.Round(3),
			Identifier: []byte("identifier2"),
			Data:       commitData,
		}

		// sign
		sig, err := km.SignRoot(msg, spectypes.QBFTSignatureType, pk.Serialize())
		require.NoError(t, err)

		// verify
		signed := &specqbft.SignedMessage{
			Signature: sig,
			Signers:   []spectypes.OperatorID{1},
			Message:   msg,
		}

		err = signed.GetSignature().VerifyByOperators(signed, types.GetDefaultDomain(), spectypes.QBFTSignatureType, []*spectypes.Operator{{OperatorID: spectypes.OperatorID(1), PubKey: pk.Serialize()}})
		// res, err := signed.VerifySig(pk)
		require.NoError(t, err)
		// require.True(t, res)
	})
}
