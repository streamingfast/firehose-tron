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
	"github.com/streamingfast/firehose-tron/rpc"
	"go.uber.org/zap"
)

var FetchCommand = Command(fetchE,
	"fetch <first-streamable-block>",
	"Fetch blocks from RPC endpoint and produce Firehose blocks for consumption by Firehose Core",
	ExactArgs(1),
	Flags(func(flags *pflag.FlagSet) {
		flags.StringArray("tron-endpoints", []string{""}, "List of endpoints to use to fetch different method calls")
		flags.String("state-dir", "/data/poller", "Directory to store state information")
		flags.Duration("interval-between-fetch", 0, "Interval between fetch operations")
		flags.Duration("latest-block-retry-interval", time.Second, "Interval between retries when fetching latest block")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "Maximum delay before considering a block fetch as failed")
	}),
)

func fetchE(cmd *cobra.Command, args []string) error {
	rpcEndpoints := sflags.MustGetStringArray(cmd, "tron-endpoints")
	stateDir := sflags.MustGetString(cmd, "state-dir")
	startBlock, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unable to parse first streamable block %d: %w", startBlock, err)
	}

	fetchInterval := sflags.MustGetDuration(cmd, "interval-between-fetch")
	maxBlockFetchDuration := sflags.MustGetDuration(cmd, "max-block-fetch-duration")
	latestBlockRetryInterval := sflags.MustGetDuration(cmd, "latest-block-retry-interval")

	zlog.Info("launching firehose-tron poller",
		zap.Strings("rpc_endpoints", rpcEndpoints),
		zap.String("state_dir", stateDir),
		zap.Uint64("first_streamable_block", startBlock),
		zap.Duration("interval_between_fetch", fetchInterval),
		zap.Duration("latest_block_retry_interval", latestBlockRetryInterval),
	)

	rpcFetcher := rpc.NewFetcher(rpcEndpoints, fetchInterval, latestBlockRetryInterval, zlog)

	poller := blockpoller.New(
		rpcFetcher,
		blockpoller.NewFireBlockHandler("type.googleapis.com/sf.tron.type.v1.Block"),
		nil, // No clients needed for Tron
		blockpoller.WithStoringState[any](stateDir),
		blockpoller.WithLogger[any](zlog),
	)

	err = poller.Run(startBlock, nil, sflags.MustGetInt(cmd, "block-fetch-batch-size"))
	if err != nil {
		return fmt.Errorf("running poller: %w", err)
	}

	return nil
}
