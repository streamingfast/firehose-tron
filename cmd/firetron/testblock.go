package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/streamingfast/cli"
	. "github.com/streamingfast/cli"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"github.com/streamingfast/firehose-tron/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var TestBlockCommand = Command(testBlockE,
	"test-block <start_block> <end_block>",
	"Test Tron block fetcher for a range of blocks",
	ExactArgs(2),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("rpc-endpoint", "grpc.trongrid.io:50051", "Tron RPC endpoint")
		flags.String("tron-api-key", "", "Tron API key for RPC access")
		flags.String("state-dir", "/data/poller", "Directory to store state information")
		flags.Duration("interval-between-fetch", 100*time.Millisecond, "Interval between block fetches (default: 100ms to stay under 15qps limit)")
		flags.Duration("latest-block-retry-interval", time.Second, "Interval between retries for latest block")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "Maximum delay before considering a block fetch as failed")
		flags.Int("max-requests-per-second", 14, "Maximum requests per second to TronGrid API (default: 14 to stay under 15qps limit)")
	}),
)

func testBlockE(cmd *cobra.Command, args []string) error {
	startBlock, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing start block number: %w", err)
	}

	endBlock, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing end block number: %w", err)
	}

	if endBlock < startBlock {
		return fmt.Errorf("end block must be greater than or equal to start block")
	}

	rpcEndpoint, _ := cmd.Flags().GetString("rpc-endpoint")
	apiKey, _ := cmd.Flags().GetString("tron-api-key")
	intervalBetweenFetch, _ := cmd.Flags().GetDuration("interval-between-fetch")
	latestBlockRetryInterval, _ := cmd.Flags().GetDuration("latest-block-retry-interval")
	batchSize, _ := cmd.Flags().GetInt("block-fetch-batch-size")
	maxBlockFetchDuration, _ := cmd.Flags().GetDuration("max-block-fetch-duration")
	maxRPS, _ := cmd.Flags().GetInt("max-requests-per-second")

	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(maxRPS), 1)

	logger.Info("testing block range conversion",
		zap.Uint64("start_block", startBlock),
		zap.Uint64("end_block", endBlock),
		zap.String("rpc_endpoint", rpcEndpoint),
		zap.Duration("interval_between_fetch", intervalBetweenFetch),
		zap.Duration("latest_block_retry_interval", latestBlockRetryInterval),
		zap.Int("batch_size", batchSize),
		zap.Duration("max_block_fetch_duration", maxBlockFetchDuration),
		zap.Int("max_requests_per_second", maxRPS),
	)

	rollingStrategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
	tronClients := firecoreRPC.NewClients(maxBlockFetchDuration, rollingStrategy, logger)
	client, err := rpc.NewTronClient(rpcEndpoint, apiKey)
	if err != nil {
		return fmt.Errorf("failed to create Tron client: %w", err)
	}
	tronClients.Add(client)

	// Create Tron clients with all endpoints
	fetcher := rpc.NewFetcher(tronClients, intervalBetweenFetch, latestBlockRetryInterval, logger)

	// Process blocks in batches
	for currentBlock := startBlock; currentBlock <= endBlock; currentBlock += uint64(batchSize) {
		batchEnd := currentBlock + uint64(batchSize) - 1
		if batchEnd > endBlock {
			batchEnd = endBlock
		}

		logger.Info("processing block batch",
			zap.Uint64("batch_start", currentBlock),
			zap.Uint64("batch_end", batchEnd),
		)

		for blockNum := currentBlock; blockNum <= batchEnd; blockNum++ {
			// Wait for rate limiter
			if err := limiter.Wait(context.Background()); err != nil {
				return fmt.Errorf("rate limiter error: %w", err)
			}

			// Create context with timeout for block fetch
			ctx, cancel := context.WithTimeout(context.Background(), maxBlockFetchDuration)
			defer cancel()

			// Fetch block
			block, skipped, err := fetcher.Fetch(ctx, client, blockNum)
			if err != nil {
				return fmt.Errorf("failed to get block %d: %w", blockNum, err)
			}

			if skipped {
				logger.Info("block was skipped", zap.Uint64("block_num", blockNum))
				continue
			}

			// Print raw block details in JSON format
			fmt.Printf("\nBlock %d Details:\n", blockNum)
			printProtoToMultilineJSON(block)

			// Sleep between fetches if interval is set
			if intervalBetweenFetch > 0 && blockNum < batchEnd {
				time.Sleep(intervalBetweenFetch)
			}
		}

		// Sleep between batches if interval is set
		if intervalBetweenFetch > 0 && batchEnd < endBlock {
			time.Sleep(intervalBetweenFetch)
		}
	}

	return nil
}

func printProtoToMultilineJSON(message proto.Message) {
	marshaler := protojson.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
	}
	jsonBytes, err := marshaler.Marshal(message)
	cli.NoError(err, "Failed to marshal proto message to JSON")

	fmt.Println(string(jsonBytes))
}
