package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	"github.com/streamingfast/firehose-tron/rpc"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

var TestBlockCommand = Command(testBlockE,
	"test-block <start_block> <end_block>",
	"Test Tron block fetcher for a range of blocks",
	ExactArgs(2),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("rpc-endpoint", "https://api.trongrid.io", "Tron RPC endpoint")
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
	if apiKey == "" {
		return fmt.Errorf("tron api key must be provided")
	}

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

	// Create Tron client
	client := rpc.NewTronClient(rpcEndpoint, apiKey)

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
			block, err := client.GetBlock(ctx, blockNum)
			if err != nil {
				return fmt.Errorf("failed to get block %d: %w", blockNum, err)
			}

			// Print block details
			fmt.Printf("\nBlock %d Details:\n", blockNum)
			fmt.Printf("Block ID: %s\n", block.BlockId)
			fmt.Printf("Block Number: %d\n", block.BlockHeader.RawData.Number)
			fmt.Printf("Parent Hash: %x\n", block.BlockHeader.RawData.ParentHash)
			fmt.Printf("Timestamp: %s\n", time.Unix(block.BlockHeader.RawData.Timestamp/1000, 0))
			fmt.Printf("Witness Address: %x\n", block.BlockHeader.RawData.WitnessAddress)
			fmt.Printf("Number of Transactions: %d\n", len(block.Transactions))

			// Fetch and verify transaction info
			var txInfos []*pbtron.TransactionInfo
			for _, tx := range block.Transactions {
				// Wait for rate limiter before each transaction info fetch
				if err := limiter.Wait(context.Background()); err != nil {
					return fmt.Errorf("rate limiter error: %w", err)
				}

				txInfo, err := client.GetTransactionInfoByBlockNum(ctx, blockNum, tx.TxId)
				if err != nil {
					return fmt.Errorf("failed to get transaction info for tx %s: %w", tx.TxId, err)
				}
				txInfos = append(txInfos, txInfo)
			}

			// Verify integrity between block and transaction info
			integrity, err := rpc.VerifyIntegrity(block, txInfos)
			if err != nil {
				return fmt.Errorf("verifying integrity for block %d: %w", blockNum, err)
			}
			if !integrity {
				return fmt.Errorf("integrity check failed for block %d: block and transaction info hashes do not match", blockNum)
			}

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
