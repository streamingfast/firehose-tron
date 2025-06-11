package main

import (
	"fmt"
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
		flags.StringArray("tron-evm-endpoints", []string{""}, "List of endpoints to use to fetch different method calls")
		flags.StringArray("tron-endpoints", []string{""}, "List of endpoints to use to fetch different method calls")
		flags.String("tron-api-key", "", "Tron API key for RPC access")
		flags.String("state-dir", "/data/poller", "Directory to store state information")
		flags.Duration("interval-between-fetch", 0, "Interval between fetch operations")
		flags.Duration("latest-block-retry-interval", time.Second, "Interval between retries when fetching latest block")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "Maximum delay before considering a block fetch as failed")
	}),
)

func fetchEVME(cmd *cobra.Command, args []string) error {
	rpcEndpoints := sflags.MustGetStringArray(cmd, "tron-endpoints")
	if len(rpcEndpoints) == 0 {
		return fmt.Errorf("at least one Tron RPC endpoint must be provided")
	}

	evmRpcEndpoints := sflags.MustGetStringArray(cmd, "tron-evm-endpoints")
	if len(evmRpcEndpoints) == 0 {
		return fmt.Errorf("at least one Tron EVM RPC endpoint must be provided")
	}

	apiKey := sflags.MustGetString(cmd, "tron-api-key")
	stateDir := sflags.MustGetString(cmd, "state-dir")
	startBlock, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unable to parse first streamable block %d: %w", startBlock, err)
	}

	fetchInterval := sflags.MustGetDuration(cmd, "interval-between-fetch")
	latestBlockRetryInterval := sflags.MustGetDuration(cmd, "latest-block-retry-interval")
	maxBlockFetchDuration := sflags.MustGetDuration(cmd, "max-block-fetch-duration")

	logger.Info("launching Tron EVM poller",
		zap.Strings("rpc_endpoints", rpcEndpoints),
		zap.Strings("rpc_evm_endpoints", evmRpcEndpoints),
		zap.String("state_dir", stateDir),
		zap.Uint64("first_streamable_block", startBlock),
		zap.Duration("interval_between_fetch", fetchInterval),
		zap.Duration("latest_block_retry_interval", latestBlockRetryInterval),
		zap.Duration("max_block_fetch_duration", maxBlockFetchDuration),
	)

	// Create Tron clients with all endpoints
	rollingStrategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
	tronClients := firecoreRPC.NewClients(maxBlockFetchDuration, rollingStrategy, logger)
	for _, endpoint := range rpcEndpoints {
		client, err := rpc.NewTronClient(endpoint, apiKey)
		if err != nil {
			return fmt.Errorf("failed to create Tron client for endpoint %q: %w", endpoint, err)
		}
		tronClients.Add(client)
	}

	// TRON native block fetcher
	tronFetcher := rpc.NewFetcher(fetchInterval, latestBlockRetryInterval, logger)

	// Create EVM clients with all endpoints
	evmRollingStrategy := firecoreRPC.NewStickyRollingStrategy[*ethRPC.Client]()
	evmClients := firecoreRPC.NewClients(maxBlockFetchDuration, evmRollingStrategy, logger)
	for _, endpoint := range evmRpcEndpoints {
		client := ethRPC.NewClient(endpoint, ethRPC.WithHttpHeader("TRON-PRO-API-KEY", "be47508c-91ec-42a1-a0f0-baa22efeecbc"))
		evmClients.Add(client)
	}

	// EVM block fetcher
	evmFetcher := rpc.NewEVMFetcher(tronClients, tronFetcher, fetchInterval, latestBlockRetryInterval, logger) //

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
