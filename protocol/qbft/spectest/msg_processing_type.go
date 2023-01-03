package qbft

import (
	"encoding/hex"
	"fmt"
	"github.com/bloxapp/ssv/protocol/qbft"
	"github.com/bloxapp/ssv/protocol/qbft/instance"
	qbfttesting "github.com/bloxapp/ssv/protocol/qbft/testing"
	"testing"
	"time"

	specqbft "github.com/bloxapp/ssv-spec/qbft"
	spectests "github.com/bloxapp/ssv-spec/qbft/spectest/tests"
	spectypes "github.com/bloxapp/ssv-spec/types"
	spectestingutils "github.com/bloxapp/ssv-spec/types/testingutils"
	"github.com/stretchr/testify/require"
)

// RunMsgProcessing processes MsgProcessingSpecTest. It probably may be removed.
func RunMsgProcessing(t *testing.T, test *spectests.MsgProcessingSpecTest) {
	// a little trick we do to instantiate all the internal instance params
	preByts, _ := test.Pre.Encode()
	msgId := specqbft.ControllerIdToMessageID(test.Pre.State.ID)
	pre := instance.NewInstance(
		qbfttesting.TestingConfig(spectestingutils.KeySetForShare(test.Pre.State.Share), msgId.GetRoleType()),
		test.Pre.State.Share,
		test.Pre.State.ID,
		test.Pre.State.Height,
	)
	require.NoError(t, pre.Decode(preByts))

	preInstance := pre

	// a simple hack to change the proposer func
	if preInstance.State.Height == spectests.ChangeProposerFuncInstanceHeight {
		preInstance.GetConfig().(*qbft.Config).ProposerF = func(state *specqbft.State, round specqbft.Round) spectypes.OperatorID {
			return 2
		}
	}

	var lastErr error
	for _, msg := range test.InputMessages {
		_, _, _, err := preInstance.ProcessMsg(msg)
		if err != nil {
			lastErr = err
		}
	}

	if len(test.ExpectedError) != 0 {
		require.EqualError(t, lastErr, test.ExpectedError)
	} else {
		require.NoError(t, lastErr)
	}

	postRoot, err := preInstance.State.GetRoot()
	require.NoError(t, err)

	// broadcasting is asynchronic, so need to wait a bit before checking
	time.Sleep(time.Millisecond * 50)

	// test output message
	broadcastedMsgs := preInstance.GetConfig().GetNetwork().(*spectestingutils.TestingNetwork).BroadcastedMsgs
	if len(test.OutputMessages) > 0 || len(broadcastedMsgs) > 0 {
		require.Len(t, broadcastedMsgs, len(test.OutputMessages))

		for i, msg := range test.OutputMessages {
			r1, _ := msg.GetRoot()

			msg2 := &specqbft.SignedMessage{}
			require.NoError(t, msg2.Decode(broadcastedMsgs[i].Data))
			r2, _ := msg2.GetRoot()

			require.EqualValues(t, r1, r2, fmt.Sprintf("output msg %d roots not equal", i))
		}
	}

	require.EqualValues(t, test.PostRoot, hex.EncodeToString(postRoot), "post root not valid")
}
