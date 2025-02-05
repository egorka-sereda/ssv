package scenarios

import (
	"context"
	"fmt"

	spectypes "github.com/bloxapp/ssv-spec/types"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/automation/commons"
	"github.com/bloxapp/ssv/automation/qbft/runner"
	qbftstorage "github.com/bloxapp/ssv/ibft/storage"
	p2pv1 "github.com/bloxapp/ssv/network/p2p"
	forksprotocol "github.com/bloxapp/ssv/protocol/forks"
	p2pprotocol "github.com/bloxapp/ssv/protocol/v1/p2p"
	qbftstorageprotocol "github.com/bloxapp/ssv/protocol/v1/qbft/storage"
	"github.com/bloxapp/ssv/protocol/v1/sync/handlers"
	"github.com/bloxapp/ssv/storage"
	"github.com/bloxapp/ssv/storage/basedb"
)

// QBFTScenarioBootstrapper bootstraps qbft scenarios
func QBFTScenarioBootstrapper() runner.Bootstrapper {
	return func(ctx context.Context, plogger *zap.Logger, scenario runner.Scenario) (*runner.ScenarioContext, error) {
		loggerFactory := func(s string) *zap.Logger {
			return plogger.With(zap.String("who", s))
		}
		logger := loggerFactory(fmt.Sprintf("Bootstrap/%s", scenario.Name()))
		logger.Info("creating resources")

		totalNodes := scenario.NumOfOperators() + scenario.NumOfFullNodes()
		dbs := make([]basedb.IDb, 0)
		for i := 0; i < totalNodes; i++ {
			db, err := storage.GetStorageFactory(basedb.Options{
				Type:   "badger-memory",
				Path:   "",
				Logger: zap.L(),
			})
			if err != nil {
				logger.Panic("could not setup storage", zap.Error(err))
			}
			dbs = append(dbs, db)
		}
		forkVersion := forksprotocol.GenesisForkVersion

		ln, err := p2pv1.CreateAndStartLocalNet(ctx, loggerFactory, forkVersion, totalNodes, totalNodes/2, scenario.NumOfBootnodes() > 0)
		if err != nil {
			return nil, err
		}
		stores := make([]qbftstorageprotocol.QBFTStore, 0)
		kms := make([]spectypes.KeyManager, 0)
		for i, node := range ln.Nodes {
			store := qbftstorage.New(dbs[i], loggerFactory(fmt.Sprintf("qbft-store-%d", i+1)), "attestations", forkVersion)
			stores = append(stores, store)
			km := commons.NewTestKeyManager()
			kms = append(kms, km)
			node.RegisterHandlers(p2pprotocol.WithHandler(
				p2pprotocol.LastDecidedProtocol,
				handlers.LastDecidedHandler(loggerFactory(fmt.Sprintf("decided-handler-%d", i+1)), store, node),
			), p2pprotocol.WithHandler(
				p2pprotocol.LastChangeRoundProtocol,
				handlers.LastChangeRoundHandler(loggerFactory(fmt.Sprintf("changeround-handler-%d", i+1)), store, node),
			), p2pprotocol.WithHandler(
				p2pprotocol.DecidedHistoryProtocol,
				handlers.HistoryHandler(loggerFactory(fmt.Sprintf("history-handler-%d", i+1)), store, node, 25),
			))
		}

		return &runner.ScenarioContext{
			Ctx:         ctx,
			LocalNet:    ln,
			Stores:      stores,
			KeyManagers: kms,
			DBs:         dbs,
		}, nil
	}
}
