package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

var TestBlockCommand = Command(testBlockE,
	"test-block <block_num> <tron-rpc-endpoint>",
	"Test Tron block fetcher",
	ExactArgs(2),
	Flags(func(flags *pflag.FlagSet) {
		flags.StringArray("tron-endpoints", []string{""}, "List of endpoints to use to fetch different method calls")
		flags.String("state-dir", "/data/poller", "interval between fetch")
		flags.Duration("interval-between-fetch", 0, "interval between fetch")
		flags.Duration("latest-block-retry-interval", time.Second, "interval between fetch")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "maximum delay before considering a block fetch as failed")
	}),
)

func testBlockE(cmd *cobra.Command, args []string) error {
	blockNum, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing block number: %w", err)
	}
	tronEndpoint := args[1]

	logger, _ := logging.PackageLogger("firetron", "github.com/streamingfast/firehose-tron")
	logger.Info("testing block conversion",
		zap.Uint64("block_num", blockNum),
		zap.String("tron_endpoint", tronEndpoint),
	)

	// TODO: Implement block testing
	// 1. Create Tron client
	// 2. Fetch block from Tron RPC
	// 3. Convert to Firehose format
	// 4. Print result

	return fmt.Errorf("not implemented")
}
