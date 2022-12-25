package goeth

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prysmaticlabs/prysm/async/event"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/eth1"
	"github.com/bloxapp/ssv/eth1/abiparser"
)

func TestEth1Client_handleEvent(t *testing.T) {
	tests := []struct {
		name           string
		version        eth1.Version
		operatorAdded  string
		validatorAdded string
	}{
		{
			name:           "v2 abi contract",
			version:        eth1.V2,
			operatorAdded:  rawOperatorAdded,
			validatorAdded: rawValidatorAdded,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ec := newEth1Client(test.version)
			contractAbi, err := abi.JSON(strings.NewReader(eth1.ContractABI(test.version)))
			require.NoError(t, err)
			require.NotNil(t, contractAbi)
			var vLogOperatorAdded types.Log
			err = json.Unmarshal([]byte(test.operatorAdded), &vLogOperatorAdded)
			require.NoError(t, err)
			var vLogValidatorAdded types.Log
			err = json.Unmarshal([]byte(test.validatorAdded), &vLogValidatorAdded)
			require.NoError(t, err)

			cn := make(chan *eth1.Event)
			sub := ec.EventsFeed().Subscribe(cn)
			require.NoError(t, err)
			var eventsWg sync.WaitGroup
			go func() {
				defer sub.Unsubscribe()
				for event := range cn {
					if ethEvent, ok := event.Data.(abiparser.OperatorAddedEvent); ok {
						require.NotNil(t, ethEvent)
						require.NotNil(t, ethEvent.PublicKey)
						eventsWg.Done()
						continue
					}
					if ethEvent, ok := event.Data.(abiparser.ValidatorAddedEvent); ok {
						require.NotNil(t, ethEvent)
						require.NotNil(t, ethEvent.PublicKey)
						eventsWg.Done()
						continue
					}
					panic("event data type is not founded")
				}
			}()

			eventsWg.Add(1)
			_, err = ec.handleEvent(vLogOperatorAdded, contractAbi)
			require.NoError(t, err)

			time.Sleep(10 * time.Millisecond)
			eventsWg.Add(1)
			_, err = ec.handleEvent(vLogValidatorAdded, contractAbi)
			require.NoError(t, err)

			eventsWg.Wait()
		})
	}
}

func newEth1Client(abiVersion eth1.Version) *eth1Client {
	ec := eth1Client{
		ctx:        context.TODO(),
		conn:       nil,
		logger:     zap.L(),
		eventsFeed: new(event.Feed),
		abiVersion: abiVersion,
	}
	return &ec
}

var rawOperatorAdded = `{
  "address": "0x2EAD684aa2E10E31370830F00E0812bE6205F5f9",
  "topics": [
	"0xd839f31c14bd632f424e307b36abff63ca33684f77f28e35dc13718ef338f7f4",
	"0x0000000000000000000000003e6935b8250cf9a777862871649e5594be08779e"
  ],
  "data": "0x00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000060000000000000000000000000000000000000000000000000000001e574e05f0000000000000000000000000000000000000000000000000000000000000002c0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000002644c5330744c5331435255644a54694253553045675546564354456c4449457446575330744c533074436b314a53554a4a616b464f516d64726357687261556335647a424351564646526b464254304e425554684254556c4a516b4e6e53304e4255555642636e4a69546d77344f5535575679395865566b3152454e694e316f4b5a306872615442524d6b63775455644b62446c43524646314f476444556d35425a326861596a68365344467a5a4852344f466c58545759345232315755464e326145343353324932636c4e47616a466a574646345267705956484255623278356254524d4b315a345155646e53316379554846454d57637264465647553231495230394661325236616e52715a557378575645766447644e5547465162324e55596a55784e453578596c5a6e436e7061516d737753537431523074305a7a42496547394f5630684f5a485178644670554e6e597a643074614d584e556347527a54306c785a5459355746564c4d6b4676517a4a4b4d314252527a6831616d78326356454b54474a3452475654516d467a5a55356b5a305a6956336b77595655785746564d5a565a724e5852714c32683153326c5057477059566d746b656e5a485754413161554a4c5653397056474e6d65575a78523167725767706d6557395959555a614d47683455446c72516b356e5657747a6157394557554e705a3230324b334a42556a6859535749335a305a4f64474e3063446b304f55593455465a79536b64735745746b4f4556334d6a5644436a68525355524255554643436930744c5330745255354549464a545153425156554a4d53554d675330565a4c5330744c53304b00000000000000000000000000000000000000000000000000000000",
  "blockNumber": "0x6E1070",
  "transactionHash": "0x79478d46847aca9aa93f351c4b9c2126739a746b916da6445c0e64ab227fd016"
}`

var rawValidatorAdded = `{
  "address": "0x2EAD684aa2E10E31370830F00E0812bE6205F5f9",
  "topics": [
	"0xd236b4a362a1dcc087f100d3c9de3d4dca2dbccf612505245ffa5444f6fc6ad0"
  ],
  "data": "0x0000000000000000000000003e6935b8250cf9a777862871649e5594be08779e00000000000000000000000000000000000000000000000000000000000001600000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000026000000000000000000000000000000000000000000000000000000000000004800000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002bf1bde00000000000000000000000000000000000000000000000000000000c6d7cf000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000030ae27224346379f1cdc5ab4ff6fcd3841ce03f867f8c73fdef913a23508a4192a53e420d7affa56d3c37f48c26af6efb3000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000000e0000000000000000000000000000000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000001a00000000000000000000000000000000000000000000000000000000000000030b023c60eb5434b92551c523c48c2c5ffcdc0da8849bb7924d5651837747893c698aaf1c062ff912226fb88945ee551ec000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000030a5b7662004130babdc821e709d46ad55d90b4e326ad3ce6bb667ddb6cb7fbf1978f7aeefa86e6064875a7b0eb1d956a8000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000030aeb35e701a0909455b8e8a518f0b4f992db9f5146949e9be145479615657a522c0e0fd7a0a27883fd6dc8b8b6c7d1947000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000030b1e05cc9b7333e4392cc09ce7fea340de01e75fb1723d7572a3a37f181ff35df509aff40a110ad850e80305ef54c498900000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000000800000000000000000000000000000000000000000000000000000000000000240000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000005c000000000000000000000000000000000000000000000000000000000000001a0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000001586d354d5a646c67743459484435384a6c72495755774741326f77534f73776e434d316f654c4f70666a5379335937673341753045786647343134614f69367a326e67665458336738476367746e4a4c516c696d7851777947764c34456d65754f5a336572447266417a626b4878336c4d5558614f455345584a57515a384e6a41396a6163497744785076737447597736514445416b68466c76524f74684c50314d74645868354f6e586d325a44546b7442486a72434e2b526a7856706c6f6267613176324175783667384f784457434561616d716a6e4d3831456f393374697a62446b5953522b55576d38496651564838394c744e3351587751743070457857436e5a47587641796b53596739336f47345646443035627a4552554769536e667437486b63416d4b77354154715937344e482f357869486a2b626e7a44706653746d78654c386251735247717a7946377541756b2b673d3d000000000000000000000000000000000000000000000000000000000000000000000000000001a0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000001586a6465764d5a4264676a43494c6f77426276736b416765544a7a586b474e7974676752516b6e6d464237756f6c4171375030313069332f386d3647446159466539676c3273474c374d4b686466584b685179754f2b3475755a342b546251744233624b6f4a714665386468324f457753647638584f775157534c5a7a656d636b57557033326f785936673864697065687868716e6e754f3556724f496e676f45625776784c5933637976364e7767542f4f6c4a41634f2b73767759425365633164756d5649547544774b6138676370426e792b66674c41664e523832354976576a317154656654666b7677366b3371644d7142385371394c38586e61424857476b436e493157524c7a767365366f6b346137516e58726339742b382f413754637a5a35382f6a725a6c5149457938525863614c41414f45684a4361756b584e4737687042584e574b5866396843694c32697566304f673d3d000000000000000000000000000000000000000000000000000000000000000000000000000001a000000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000158634c642f334630525a7a7349367279416a6946362b55566941656935575476485856784f745758656753586863527a67464f5167562b4f5a334637345339375850626367576e53426676474578534b4239347872417179682b6e3743424e695769372b526b41307772734763323744442f5954363441797652492b45424f4939726a6b33355552724b4f54467665737952315535426a6e7a5551795849654c43463150504c54744f65426e2f3554574936434a64384961376a4c436c563677386b7a4570734d483554396763676c48624a712b35686f6d694e6c772f39764a67516c516b45587844536f73505236646c50566a6b50426254314b6f333636596677485056735441354e4f744c6f797a674c4734514f556d56434f366c4b332f444e43345055747461436e48384b6941796f59787047384d67484f324a4e452b616e41394a57576d42653371656b6c4d6c477a424c6c673d3d000000000000000000000000000000000000000000000000000000000000000000000000000001a000000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000158552f4a4365414a61614842367848465274696c584b4c704277314a4363545153334d584e2b69727267786850787a537944437a57377a536c342b506e5848612b63504e684632546272396a2b4f364e503648386a326965724665475a577058366b682b6b50424d447962496e7a78795654366d534c385a7a632b38673359592f49456b6a4e583672515a71337039587738303348694c31527a4c366a3746475573496f79664e7750596471712b41574f6e4c724a4833336b747a452b63485742586f507945616e504c71444931634c2b2f574f5037426e43563339494b35703573466a794f4e75314b7352616f49573867667a537442425673587737495842524a466b5a7a7345775a43624c484d45754145522b3952546f6f4c76336e326741656f452b6e6a31435a614f53564759545271546e6a48464a30797046576f667139774c4879766b7a664a2b5935797863726254664d673d3d0000000000000000",
  "blockNumber": "0x6E10A0",
  "transactionHash": "0x836169107c9e68eb9372daf220281b73552a6fcd99f188ca4335029d2513439d"
}`
