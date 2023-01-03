package qbft

import (
	"encoding/json"
	"github.com/bloxapp/ssv-spec/qbft/spectest/tests/timeout"
	"github.com/bloxapp/ssv-spec/types/testingutils"
	"github.com/bloxapp/ssv/protocol/qbft/instance"
	testing2 "github.com/bloxapp/ssv/protocol/qbft/testing"
	"github.com/bloxapp/ssv/protocol/types"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	spectests "github.com/bloxapp/ssv-spec/qbft/spectest/tests"
	"github.com/bloxapp/ssv-spec/qbft/spectest/tests/controller/futuremsg"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/bloxapp/ssv/utils/logex"
)

func init() {
	logex.Build("qbft-mapping-test", zapcore.DebugLevel, nil)
}

func TestQBFTMapping(t *testing.T) {
	path, _ := os.Getwd()
	fileName := "tests.json"
	filePath := path + "/" + fileName
	jsonTests, err := os.ReadFile(filePath)
	if err != nil {
		resp, err := http.Get("https://raw.githubusercontent.com/bloxapp/ssv-spec/main/qbft/spectest/generate/tests.json")
		require.NoError(t, err)

		defer func() {
			require.NoError(t, resp.Body.Close())
		}()

		jsonTests, err = io.ReadAll(resp.Body)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filePath, jsonTests, 0644))
	}

	untypedTests := map[string]interface{}{}
	if err := json.Unmarshal(jsonTests, &untypedTests); err != nil {
		panic(err.Error())
	}

	origDomain := types.GetDefaultDomain()
	types.SetDefaultDomain(spectypes.PrimusTestnet)
	defer func() {
		types.SetDefaultDomain(origDomain)
	}()

	for name, test := range untypedTests {
		logex.Reset()
		name, test := name, test

		testName := strings.Split(name, "_")[1]
		testType := strings.Split(name, "_")[0]

		switch testType {
		case reflect.TypeOf(&spectests.MsgProcessingSpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &spectests.MsgProcessingSpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			t.Run(typedTest.TestName(), func(t *testing.T) {
				RunMsgProcessing(t, typedTest)
			})
		case reflect.TypeOf(&spectests.MsgSpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &spectests.MsgSpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			t.Run(typedTest.TestName(), func(t *testing.T) {
				RunMsg(t, typedTest)
			})
		case reflect.TypeOf(&spectests.ControllerSpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &spectests.ControllerSpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			t.Run(typedTest.TestName(), func(t *testing.T) {
				RunControllerSpecTest(t, typedTest)
			})
		case reflect.TypeOf(&spectests.CreateMsgSpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &spectests.CreateMsgSpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			t.Run(typedTest.TestName(), func(t *testing.T) {
				RunCreateMsg(t, typedTest)
			})
		case reflect.TypeOf(&spectests.RoundRobinSpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &spectests.RoundRobinSpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			t.Run(typedTest.TestName(), func(t *testing.T) { // using only spec struct so no need to run our version (TODO: check how we choose leader)
				typedTest.Run(t)
			})
			/*t.Run(typedTest.TestName(), func(t *testing.T) {
				RunMsg(t, typedTest)
			})*/

		case reflect.TypeOf(&futuremsg.ControllerSyncSpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &futuremsg.ControllerSyncSpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			t.Run(typedTest.TestName(), func(t *testing.T) {
				RunControllerSync(t, typedTest)
			})
		case reflect.TypeOf(&timeout.SpecTest{}).String():
			byts, err := json.Marshal(test)
			require.NoError(t, err)
			typedTest := &SpecTest{}
			require.NoError(t, json.Unmarshal(byts, &typedTest))

			// a little trick we do to instantiate all the internal instance params

			identifier := spectypes.MessageIDFromBytes(typedTest.Pre.State.ID)
			preByts, _ := typedTest.Pre.Encode()
			pre := instance.NewInstance(
				testing2.TestingConfig(testingutils.KeySetForShare(typedTest.Pre.State.Share), identifier.GetRoleType()),
				typedTest.Pre.State.Share,
				typedTest.Pre.State.ID,
				typedTest.Pre.State.Height,
			)
			err = pre.Decode(preByts)
			require.NoError(t, err)
			typedTest.Pre = pre

			RunTimeout(t, typedTest)
		default:
			t.Fatalf("unsupported test type %s [%s]", testType, testName)
		}
	}
}
