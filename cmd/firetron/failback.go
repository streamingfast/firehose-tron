package main

import (
	"time"

	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"go.uber.org/zap"
)

const failbackIntervalFlag = "providers-failback-interval"

const failbackIntervalUsage = "Interval at which the declared endpoint order is re-preferred, " +
	"moving polling back to the preferred endpoint after a transient error moved it to a fallback one. " +
	"Set to 0 to stick to the fallback endpoint until the process is restarted"

// The pools roll to the next endpoint on error and stay there, so one transient
// failure pins polling to a fallback until the process restarts.
func startEndpointFailback[C any](
	logger *zap.Logger,
	kind string,
	clients *firecoreRPC.Clients[C],
	endpointCount int,
	interval time.Duration,
) {
	if !failbackEnabled(endpointCount, interval) {
		return
	}

	go func() {
		for range time.Tick(interval) {
			logger.Debug("re-preferring declared endpoint order", zap.String("kind", kind))
			clients.Reset()
		}
	}()
}

func failbackEnabled(endpointCount int, interval time.Duration) bool {
	return interval > 0 && endpointCount > 1
}
