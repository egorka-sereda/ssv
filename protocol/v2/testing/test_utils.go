package testing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/herumi/bls-eth-go-binary/bls"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/mod/modfile"

	qbftstorage "github.com/bloxapp/ssv/protocol/v2/qbft/storage"
	"github.com/bloxapp/ssv/protocol/v2/types"
	"github.com/bloxapp/ssv/storage/basedb"
	"github.com/bloxapp/ssv/storage/kv"
)

// TODO: add missing tests

// GenerateBLSKeys generates randomly nodes
func GenerateBLSKeys(oids ...spectypes.OperatorID) (map[spectypes.OperatorID]*bls.SecretKey, []*spectypes.Operator) {
	_ = bls.Init(bls.BLS12_381)

	nodes := make([]*spectypes.Operator, 0)
	sks := make(map[spectypes.OperatorID]*bls.SecretKey)

	for i, oid := range oids {
		sk := &bls.SecretKey{}
		sk.SetByCSPRNG()

		nodes = append(nodes, &spectypes.Operator{
			OperatorID: spectypes.OperatorID(i),
			PubKey:     sk.GetPublicKey().Serialize(),
		})
		sks[oid] = sk
	}

	return sks, nodes
}

// MsgGenerator represents a message generator
type MsgGenerator func(height specqbft.Height) ([]spectypes.OperatorID, *specqbft.Message)

// CreateMultipleStoredInstances enables to create multiple stored instances (with decided messages).
func CreateMultipleStoredInstances(
	sks map[spectypes.OperatorID]*bls.SecretKey,
	start specqbft.Height,
	end specqbft.Height,
	generator MsgGenerator,
) ([]*qbftstorage.StoredInstance, error) {
	results := make([]*qbftstorage.StoredInstance, 0)
	for i := start; i <= end; i++ {
		signers, msg := generator(i)
		if msg == nil {
			break
		}
		sm, err := MultiSignMsg(sks, signers, msg)
		if err != nil {
			return nil, err
		}
		results = append(results, &qbftstorage.StoredInstance{
			State: &specqbft.State{
				ID:                   sm.Message.Identifier,
				Round:                sm.Message.Round,
				Height:               sm.Message.Height,
				LastPreparedRound:    sm.Message.Round,
				LastPreparedValue:    sm.Message.Data,
				Decided:              true,
				DecidedValue:         sm.Message.Data,
				ProposeContainer:     specqbft.NewMsgContainer(),
				PrepareContainer:     specqbft.NewMsgContainer(),
				CommitContainer:      specqbft.NewMsgContainer(),
				RoundChangeContainer: specqbft.NewMsgContainer(),
			},
			DecidedMessage: sm,
		})
	}
	return results, nil
}

func signMessage(msg *specqbft.Message, sk *bls.SecretKey) (*bls.Sign, error) {
	signatureDomain := spectypes.ComputeSignatureDomain(types.GetDefaultDomain(), spectypes.QBFTSignatureType)
	root, err := spectypes.ComputeSigningRoot(msg, signatureDomain)
	if err != nil {
		return nil, err
	}
	return sk.SignByte(root), nil
}

// MultiSignMsg signs a msg with multiple signers
func MultiSignMsg(sks map[spectypes.OperatorID]*bls.SecretKey, signers []spectypes.OperatorID, msg *specqbft.Message) (*specqbft.SignedMessage, error) {
	_ = bls.Init(bls.BLS12_381)

	var operators = make([]spectypes.OperatorID, 0)
	var agg *bls.Sign
	for _, oid := range signers {
		signature, err := signMessage(msg, sks[oid])
		if err != nil {
			return nil, err
		}
		operators = append(operators, oid)
		if agg == nil {
			agg = signature
		} else {
			agg.Add(signature)
		}
	}

	return &specqbft.SignedMessage{
		Message:   msg,
		Signature: agg.Serialize(),
		Signers:   operators,
	}, nil
}

// SignMsg handle MultiSignMsg error and return just specqbft.SignedMessage
func SignMsg(t *testing.T, sks map[spectypes.OperatorID]*bls.SecretKey, signers []spectypes.OperatorID, msg *specqbft.Message) *specqbft.SignedMessage {
	res, err := MultiSignMsg(sks, signers, msg)
	require.NoError(t, err)
	return res
}

// AggregateSign sign specqbft.Message and then aggregate
func AggregateSign(t *testing.T, sks map[spectypes.OperatorID]*bls.SecretKey, signers []spectypes.OperatorID, consensusMessage *specqbft.Message) *specqbft.SignedMessage {
	signedMsg := SignMsg(t, sks, signers, consensusMessage)
	// TODO: use SignMsg instead of AggregateSign
	// require.NoError(t, sigSignMsgnedMsg.Aggregate(signedMsg))
	return signedMsg
}

// AggregateInvalidSign sign specqbft.Message and then change the signer id to mock invalid sig
func AggregateInvalidSign(t *testing.T, sks map[spectypes.OperatorID]*bls.SecretKey, consensusMessage *specqbft.Message) *specqbft.SignedMessage {
	sigend := SignMsg(t, sks, []spectypes.OperatorID{1}, consensusMessage)
	sigend.Signers = []spectypes.OperatorID{2}
	return sigend
}

// NewInMemDb returns basedb.IDb with in-memory type
func NewInMemDb() basedb.IDb {
	db, _ := kv.New(basedb.Options{
		Type:   "badger-memory",
		Path:   "",
		Logger: zap.L(),
	})
	return db
}

// CommitDataToBytes encode commit data and handle error if exist
func CommitDataToBytes(t *testing.T, input *specqbft.CommitData) []byte {
	ret, err := json.Marshal(input)
	require.NoError(t, err)
	return ret
}

func GetSpecTestJSON(path string, module string) ([]byte, error) {
	fileName := "tests.json"
	filePath := path + "/" + fileName
	jsonTests, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		rootPath := path
		for {
			if _, err := os.Stat(filepath.Join(rootPath, "go.mod")); err == nil {
				break
			}
			rootPath = filepath.Dir(rootPath)
		}
		buf, err := os.ReadFile(fmt.Sprintf("%s/go.mod", rootPath))
		if err != nil {
			return nil, errors.New("could not read go.mod")
		}
		goModFile, err := modfile.Parse("go.mod", buf, nil)
		if err != nil {
			return nil, errors.New("could not parse go.mod")
		}
		var req *modfile.Require
		for _, r := range goModFile.Require {
			if strings.EqualFold("github.com/bloxapp/ssv-spec", r.Mod.Path) {
				req = r
				break
			}
		}
		if req == nil {
			return nil, errors.New("could not find ssv-spec module")
		}
		var version string
		splitModVersion := strings.Split(req.Mod.Version, "-")
		if len(splitModVersion) > 1 {
			version = splitModVersion[len(splitModVersion)-1]
		} else {
			version = splitModVersion[0]
		}

		resp, err := http.Get(fmt.Sprintf("https://raw.githubusercontent.com/bloxapp/ssv-spec/%s/%s/spectest/generate/tests.json", version, module))
		if err != nil {
			return nil, errors.New("could not get tests.json")
		}

		defer func() {
			err := resp.Body.Close()
			if err != nil {
				return
			}
		}()

		jsonTests, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		err = os.WriteFile(filePath, jsonTests, 0600)
		if err != nil {
			return nil, err
		}
	}
	return jsonTests, nil
}
