package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/firehose-tron/rpc"
	"go.uber.org/zap"
)

var TestBlockCommand = Command(testBlockE,
	"test-block <block_num>",
	"Test Tron block fetcher",
	ExactArgs(1),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("rpc-endpoint", "https://api.trongrid.io", "Tron RPC endpoint")
		flags.String("tron-api-key", "", "Tron API key for RPC access")
	}),
)

func testBlockE(cmd *cobra.Command, args []string) error {
	blockNum, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing block number: %w", err)
	}

	rpcEndpoint, _ := cmd.Flags().GetString("rpc-endpoint")
	apiKey, _ := cmd.Flags().GetString("tron-api-key")
	if apiKey == "" {
		return fmt.Errorf("tron api key must be provided")
	}

	logger.Info("testing block conversion",
		zap.Uint64("block_num", blockNum),
		zap.String("rpc_endpoint", rpcEndpoint),
	)

	// Create Tron client
	client := rpc.NewTronClient(rpcEndpoint, apiKey)

	// Fetch block
	block, err := client.GetBlock(context.Background(), blockNum)
	if err != nil {
		return fmt.Errorf("failed to get block: %w", err)
	}

	// Print block details
	fmt.Println("\nBlock Details:")
	fmt.Printf("Block ID: %s\n", block.BlockId)
	fmt.Printf("Block Number: %d\n", block.BlockHeader.RawData.Number)
	fmt.Printf("Parent Hash: %x\n", block.BlockHeader.RawData.ParentHash)
	fmt.Printf("Timestamp: %s\n", time.Unix(block.BlockHeader.RawData.Timestamp/1000, 0))
	fmt.Printf("Witness Address: %x\n", block.BlockHeader.RawData.WitnessAddress)
	fmt.Printf("Number of Transactions: %d\n", len(block.Transactions))

	// Print basic transaction details
	fmt.Println("\nTransaction Details:")
	for i, tx := range block.Transactions {
		fmt.Printf("\nTransaction %d:\n", i+1)
		fmt.Printf("  ID: %s\n", tx.TxId)
		if len(tx.RawData.Contract) > 0 {
			fmt.Printf("  Type: %s\n", tx.RawData.Contract[0].Type)
		}
	}

	return nil
}
