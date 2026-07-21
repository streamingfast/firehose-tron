package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	"github.com/streamingfast/firehose-tron/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"go.uber.org/zap"
)

var FetchCommand = Command(fetchE,
	"fetch <first-streamable-block>",
	"Fetch blocks from RPC endpoint and produce Firehose blocks for consumption by Firehose Core",
	ExactArgs(1),
	Flags(func(flags *pflag.FlagSet) {
		flags.StringArray("tron-endpoints", nil, "List of endpoints to fetch from; each may carry its own key via ?apiKey=..., skip TLS validation via ?insecure=true, use http:// for plaintext, and supports ${ENV} interpolation")
		flags.String("tron-api-key", "", "DEPRECATED: default Tron API key applied to endpoints without their own; use ?apiKey=... in the endpoint URL instead (supports ${ENV}). Will be removed in a future release")
		flags.String("state-dir", "/data/poller", "Directory to store state information")
		flags.Duration("interval-between-fetch", 0, "Interval between fetch operations")
		flags.Duration("latest-block-retry-interval", time.Second, "Interval between retries when fetching latest block")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "Maximum delay before considering a block fetch as failed")
	}),
)

func fetchE(cmd *cobra.Command, args []string) error {
	rpcEndpoints := sflags.MustGetStringArray(cmd, "tron-endpoints")

	apiKey := sflags.MustGetString(cmd, "tron-api-key")
	warnDeprecatedAPIKeyFlag(logger, apiKey)
	stateDir := sflags.MustGetString(cmd, "state-dir")
	startBlock, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unable to parse first streamable block %d: %w", startBlock, err)
	}

	fetchInterval := sflags.MustGetDuration(cmd, "interval-between-fetch")
	latestBlockRetryInterval := sflags.MustGetDuration(cmd, "latest-block-retry-interval")
	maxBlockFetchDuration := sflags.MustGetDuration(cmd, "max-block-fetch-duration")

	endpoints, err := parseTronEndpoints(rpcEndpoints, apiKey)
	if err != nil {
		return err
	}

	redacted := make([]string, len(endpoints))
	for i, ep := range endpoints {
		redacted[i] = ep.String()
	}

	logger.Info("launching Tron poller",
		zap.Strings("rpc_endpoints", redacted),
		zap.String("state_dir", stateDir),
		zap.Uint64("first_streamable_block", startBlock),
		zap.Duration("interval_between_fetch", fetchInterval),
		zap.Duration("latest_block_retry_interval", latestBlockRetryInterval),
		zap.Duration("max_block_fetch_duration", maxBlockFetchDuration),
	)

	rollingStrategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
	tronClients := firecoreRPC.NewClients(maxBlockFetchDuration, rollingStrategy, logger)
	for _, ep := range endpoints {
		client, err := rpc.NewTronClient(ep)
		if err != nil {
			return fmt.Errorf("failed to create Tron client for endpoint %q: %w", ep.String(), err)
		}
		tronClients.Add(client)
	}

	// Create Tron clients with all endpoints
	fetcher := rpc.NewFetcher(fetchInterval, latestBlockRetryInterval, logger)

	poller := blockpoller.New(
		fetcher,
		blockpoller.NewFireBlockHandler(blockTypeURL()),
		tronClients,
		blockpoller.WithStoringState[pbtronapi.WalletClient](stateDir),
		blockpoller.WithLogger[pbtronapi.WalletClient](logger),
	)

	err = poller.Run(startBlock, nil, sflags.MustGetInt(cmd, "block-fetch-batch-size"))
	if err != nil {
		return fmt.Errorf("running poller: %w", err)
	}

	return nil
}

var blockTypeFullName = new(pbtron.Block).ProtoReflect().Descriptor().FullName()

func blockTypeURL() string {
	return "type.googleapis.com/" + string(blockTypeFullName)
}

// warnDeprecatedAPIKeyFlag logs a warning when the deprecated --tron-api-key
// flag is used, pointing at the per-endpoint ?apiKey= replacement.
func warnDeprecatedAPIKeyFlag(logger *zap.Logger, apiKey string) {
	if apiKey != "" {
		logger.Warn("the --tron-api-key flag is deprecated and will be removed in a future release; move the key into each endpoint URL as a query parameter instead, e.g. 'https://provider.io?apiKey=${YOUR_KEY}' (environment variables are interpolated)")
	}
}

// parseTronEndpoints parses each raw endpoint into an rpc.Endpoint, applying
// defaultAPIKey to endpoints that carry no apiKey of their own.
func parseTronEndpoints(rawEndpoints []string, defaultAPIKey string) ([]rpc.Endpoint, error) {
	if len(rawEndpoints) == 0 {
		return nil, fmt.Errorf("at least one Tron RPC endpoint must be provided")
	}

	endpoints := make([]rpc.Endpoint, 0, len(rawEndpoints))
	for _, raw := range rawEndpoints {
		ep, err := rpc.ParseEndpoint(raw, defaultAPIKey)
		if err != nil {
			return nil, fmt.Errorf("parse tron endpoint %q: %w", rpc.RedactRawURL(raw), err)
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, nil
}
