package protcolp2p

import (
	"context"
	spectypes "github.com/bloxapp/ssv-spec/types"
	"go.uber.org/zap"
	"time"
)

// WaitForMinPeers waits until there are minPeers conntected for the given validator
func WaitForMinPeers(pctx context.Context, logger *zap.Logger, subscriber Subscriber, vpk spectypes.ValidatorPK, minPeers int, interval time.Duration) error {
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()

	for ctx.Err() == nil {
		time.Sleep(interval)
		peers, err := subscriber.Peers(vpk)
		if err != nil {
			logger.Warn("could not get peers of topic", zap.Error(err))
			continue
		}
		n := len(peers)
		if n >= minPeers {
			return nil
		}
		logger.Debug("looking for min peers", zap.Int("expected", minPeers), zap.Int("actual", n))
	}

	return ctx.Err()
}
