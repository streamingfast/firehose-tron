package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ethRPC "github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/firehose-tron/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"go.uber.org/zap"
)

// preflightTimeout bounds a single endpoint probe. It is deliberately larger
// than --max-block-fetch-duration: a slow-but-working provider must not be
// reported as broken at startup.
const preflightTimeout = 10 * time.Second

// endpointProbe is a single head-block call against one endpoint, named for
// logging (the name must already be redacted).
type endpointProbe struct {
	name  string
	probe func(context.Context) (uint64, error)
}

// probeEndpoints calls every probe once so a misconfigured endpoint surfaces at
// startup instead of as a poller frozen on its first block: the poller retries
// failed fetches forever, so a wrong API key or an unreachable host never
// produces a fatal error on its own.
//
// A failing endpoint is logged; only an all-failing set is an error, since the
// poller can legitimately run with part of its providers down.
func probeEndpoints(ctx context.Context, logger *zap.Logger, kind string, probes []endpointProbe) error {
	if len(probes) == 0 {
		return nil
	}

	var failures []error
	for _, endpoint := range probes {
		probeCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
		headBlockNum, err := endpoint.probe(probeCtx)
		cancel()

		if err != nil {
			fields := []zap.Field{
				zap.String("kind", kind),
				zap.String("endpoint", endpoint.name),
				zap.Error(err),
			}
			if hint := rpc.FailureHint(err); hint != "" {
				fields = append(fields, zap.String("hint", hint))
				err = fmt.Errorf("%w (%s)", err, hint)
			}

			logger.Warn("endpoint is not reachable", fields...)
			failures = append(failures, fmt.Errorf("%s endpoint %q: %w", kind, endpoint.name, err))
			continue
		}

		logger.Info("endpoint is reachable",
			zap.String("kind", kind),
			zap.String("endpoint", endpoint.name),
			zap.Uint64("head_block_num", headBlockNum),
		)
	}

	if len(failures) == len(probes) {
		return fmt.Errorf("every %s endpoint failed its startup check, refusing to start (check the API key and the endpoint URL): %w", kind, errors.Join(failures...))
	}

	return nil
}

// tronHeadBlockProbe returns the head block number of the Tron gRPC endpoint
// backing client.
func tronHeadBlockProbe(client pbtronapi.WalletClient) func(context.Context) (uint64, error) {
	return func(ctx context.Context) (uint64, error) {
		block, err := client.GetNowBlock2(ctx, &pbtronapi.EmptyMessage{})
		if err != nil {
			return 0, err
		}

		if block.GetBlockHeader().GetRawData() == nil {
			return 0, fmt.Errorf("endpoint answered without a block header")
		}

		return uint64(block.BlockHeader.RawData.Number), nil
	}
}

// evmHeadBlockProbe returns the head block number of the EVM JSON-RPC endpoint
// backing client.
func evmHeadBlockProbe(client *ethRPC.Client) func(context.Context) (uint64, error) {
	return func(ctx context.Context) (uint64, error) {
		return client.LatestBlockNum(ctx)
	}
}

// validateAPIKeyFlag rejects a --tron-api-key value that is an unexpanded
// variable reference. Such a value is accepted by every provider's TLS
// handshake and only fails later as an authentication error inside the poller's
// infinite retry loop, which reads as a hang rather than as a misconfiguration.
func validateAPIKeyFlag(apiKey string) error {
	trimmed := strings.TrimSpace(apiKey)

	if strings.HasPrefix(trimmed, "$(") && strings.HasSuffix(trimmed, ")") {
		name := trimmed[2 : len(trimmed)-1]
		return fmt.Errorf("--tron-api-key is the unexpanded reference %q: whatever launched this process did not substitute it, the $(...) form is not expanded by us either (a shell would need $%s, a Makefile a defined variable)", apiKey, name)
	}

	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") {
		name := trimmed[2 : len(trimmed)-1]
		return fmt.Errorf("--tron-api-key is the unexpanded reference %q: whatever launched this process did not substitute it (the flag value is used verbatim, only endpoint URLs interpolate ${%s})", apiKey, name)
	}

	return nil
}
