package instance

import (
	"testing"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/protocol/v1/blockchain/beacon"
	"github.com/bloxapp/ssv/protocol/v1/qbft"
	"github.com/bloxapp/ssv/protocol/v1/qbft/instance/msgcont"
	"github.com/bloxapp/ssv/protocol/v1/qbft/instance/msgcont/inmem"
)

func TestPreparedAggregatedMsg(t *testing.T) {
	sks, nodes, operatorIds, shareOperatorIds := GenerateNodes(4)

	instance := &Instance{
		ContainersMap: map[specqbft.MessageType]msgcont.MessageContainer{
			specqbft.PrepareMsgType: inmem.New(3, 2),
		},
		Config: qbft.DefaultConsensusParams(),
		ValidatorShare: &beacon.Share{
			Committee:   nodes,
			NodeID:      operatorIds[0],
			OperatorIds: shareOperatorIds,
		},
		State:  &qbft.State{},
		Logger: zap.L(),
	}

	instance.GetState().Round.Store(specqbft.Round(1))
	instance.GetState().PreparedValue.Store([]byte(nil))
	instance.GetState().PreparedRound.Store(specqbft.Round(0))

	// not prepared
	_, err := instance.PreparedAggregatedMsg()
	require.EqualError(t, err, "state not prepared")

	// set prepared state
	instance.GetState().PreparedRound.Store(specqbft.Round(1))
	instance.GetState().PreparedValue.Store([]byte("value"))

	// test prepared but no msgs
	_, err = instance.PreparedAggregatedMsg()
	require.EqualError(t, err, "no prepare msgs")

	// test valid aggregation
	consensusMessage1 := &specqbft.Message{
		MsgType:    specqbft.PrepareMsgType,
		Round:      1,
		Identifier: []byte("Identifier"),
		Data:       prepareDataToBytes(t, &specqbft.PrepareData{Data: []byte("value")}),
	}

	prepareData, err := consensusMessage1.GetPrepareData()
	require.NoError(t, err)

	instance.ContainersMap[specqbft.PrepareMsgType].AddMessage(SignMsg(t, operatorIds[:1], sks[operatorIds[0]], consensusMessage1), prepareData.Data)
	instance.ContainersMap[specqbft.PrepareMsgType].AddMessage(SignMsg(t, operatorIds[1:2], sks[operatorIds[1]], consensusMessage1), prepareData.Data)
	instance.ContainersMap[specqbft.PrepareMsgType].AddMessage(SignMsg(t, operatorIds[2:3], sks[operatorIds[2]], consensusMessage1), prepareData.Data)

	// test aggregation
	msg, err := instance.PreparedAggregatedMsg()
	require.NoError(t, err)
	require.ElementsMatch(t, operatorIds[:3], msg.Signers)

	// test that doesn't aggregate different value
	consensusMessage2 := &specqbft.Message{
		MsgType:    specqbft.PrepareMsgType,
		Round:      1,
		Identifier: []byte("Identifier"),
		Data:       prepareDataToBytes(t, &specqbft.PrepareData{Data: []byte("value2")}),
	}
	instance.ContainersMap[specqbft.PrepareMsgType].AddMessage(SignMsg(t, operatorIds[3:4], sks[operatorIds[3]], consensusMessage2), prepareData.Data)
	msg, err = instance.PreparedAggregatedMsg()
	require.NoError(t, err)
	require.ElementsMatch(t, operatorIds[:3], msg.Signers)
}

func TestPreparePipeline(t *testing.T) {
	sks, nodes, operatorIds, shareOperatorIds := GenerateNodes(4)

	instance := &Instance{
		ContainersMap: map[specqbft.MessageType]msgcont.MessageContainer{
			specqbft.PrepareMsgType: inmem.New(3, 2),
		},
		Config: qbft.DefaultConsensusParams(),
		ValidatorShare: &beacon.Share{
			Committee:   nodes,
			NodeID:      operatorIds[0],
			PublicKey:   sks[operatorIds[0]].GetPublicKey(),
			OperatorIds: shareOperatorIds,
		},
		State: &qbft.State{},
	}

	instance.GetState().Round.Store(specqbft.Round(1))
	instance.GetState().Identifier.Store([]byte{})
	instance.GetState().Height.Store(specqbft.Height(0))

	instance.fork = testingFork(instance)
	pipeline := instance.PrepareMsgPipeline()
	// TODO: fix bad-looking name
	require.EqualValues(t, "combination of: combination of: combination of: basic msg validation, type check, identifier, sequence, authorize, , add prepare msg, , upon prepare msg, ", pipeline.Name())
}
