package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"github.com/streamingfast/firehose-tron/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"go.uber.org/zap"
)

var FetchEVMCommand = Command(fetchEVME,
	"fetch-evm <first-streamable-block>",
	"Fetch blocks from gRPC endpoint AND JSONRPC endpoint to produce evm-compatible firehose blocks for consumption by Firehose Ethereum",
	ExactArgs(1),
	Flags(func(flags *pflag.FlagSet) {
		flags.StringArray("tron-evm-endpoints", nil, "List of EVM (JSON-RPC) endpoints; each may carry its own key via ?apiKey=..., skip TLS validation via ?insecure=true, and supports ${ENV} interpolation")
		flags.StringArray("tron-endpoints", nil, "List of Tron gRPC endpoints; each may carry its own key via ?apiKey=..., skip TLS validation via ?insecure=true, use http:// for plaintext, and supports ${ENV} interpolation")
		flags.String("tron-api-key", "", "DEPRECATED: default Tron API key applied to endpoints without their own; use ?apiKey=... in the endpoint URL instead (supports ${ENV}). Will be removed in a future release")
		flags.String("state-dir", "/data/poller", "Directory to store state information")
		flags.Duration("interval-between-fetch", 0, "Interval between fetch operations")
		flags.Duration("latest-block-retry-interval", time.Second, "Interval between retries when fetching latest block")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "Maximum delay before considering a block fetch as failed")
	}),
)

func fetchEVME(cmd *cobra.Command, args []string) error {
	rpcEndpoints := sflags.MustGetStringArray(cmd, "tron-endpoints")
	evmRpcEndpoints := sflags.MustGetStringArray(cmd, "tron-evm-endpoints")

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

	tronEndpoints, err := parseTronEndpoints(rpcEndpoints, apiKey)
	if err != nil {
		return err
	}

	evmParsed := make([]rpc.Endpoint, 0, len(evmRpcEndpoints))
	for _, raw := range evmRpcEndpoints {
		ep, err := rpc.ParseEndpoint(raw, apiKey)
		if err != nil {
			return fmt.Errorf("parse tron-evm endpoint %q: %w", rpc.RedactRawURL(raw), err)
		}
		evmParsed = append(evmParsed, ep)
	}
	if len(evmParsed) == 0 {
		return fmt.Errorf("at least one Tron EVM RPC endpoint must be provided")
	}

	redactedTron := make([]string, len(tronEndpoints))
	for i, ep := range tronEndpoints {
		redactedTron[i] = ep.String()
	}
	redactedEVM := make([]string, len(evmParsed))
	for i, ep := range evmParsed {
		redactedEVM[i] = ep.String()
	}

	logger.Info("launching Tron EVM poller",
		zap.Strings("rpc_endpoints", redactedTron),
		zap.Strings("rpc_evm_endpoints", redactedEVM),
		zap.String("state_dir", stateDir),
		zap.Uint64("first_streamable_block", startBlock),
		zap.Duration("interval_between_fetch", fetchInterval),
		zap.Duration("latest_block_retry_interval", latestBlockRetryInterval),
		zap.Duration("max_block_fetch_duration", maxBlockFetchDuration),
	)

	// Create Tron clients with all endpoints
	rollingStrategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
	tronClients := firecoreRPC.NewClients(maxBlockFetchDuration, rollingStrategy, logger)
	for _, ep := range tronEndpoints {
		client, err := rpc.NewTronClient(ep)
		if err != nil {
			return fmt.Errorf("failed to create Tron client for endpoint %q: %w", ep.String(), err)
		}
		tronClients.Add(client)
	}

	// TRON native block fetcher
	tronFetcher := rpc.NewFetcher(fetchInterval, latestBlockRetryInterval, logger)

	// Create EVM clients with all endpoints
	evmRollingStrategy := firecoreRPC.NewStickyRollingStrategy[*ethRPC.Client]()
	evmClients := firecoreRPC.NewClients(maxBlockFetchDuration, evmRollingStrategy, logger)
	for _, ep := range evmParsed {
		evmClients.Add(newEVMClient(ep))
	}

	// EVM block fetcher
	evmFetcher := rpc.NewEVMFetcher(tronClients, tronFetcher, fetchInterval, latestBlockRetryInterval, logger)

	poller := blockpoller.New(
		evmFetcher,
		blockpoller.NewFireBlockHandler("type.googleapis.com/sf.ethereum.type.v2.Block"),
		evmClients,
		blockpoller.WithStoringState[*ethRPC.Client](stateDir),
		blockpoller.WithLogger[*ethRPC.Client](logger),
	)

	err = poller.Run(startBlock, nil, sflags.MustGetInt(cmd, "block-fetch-batch-size"))
	if err != nil {
		return fmt.Errorf("running poller: %w", err)
	}

	return nil
}

// newEVMClient builds an EVM JSON-RPC client for ep, sending the per-endpoint
// API key as request headers (never in the URL) and honoring ep.Insecure by
// skipping certificate validation.
func newEVMClient(ep rpc.Endpoint) *ethRPC.Client {
	opts := []ethRPC.Option{}
	if ep.APIKey != "" {
		opts = append(opts,
			ethRPC.WithHttpHeader("TRON-PRO-API-KEY", ep.APIKey),
			ethRPC.WithHttpHeader("x-token", ep.APIKey),
		)
	}
	if ep.Insecure {
		opts = append(opts, ethRPC.WithHttpClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}))
	}
	return ethRPC.NewClient(ep.URL.String(), opts...)
}
