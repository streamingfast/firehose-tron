package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/firehose-tron/rpc"
	"golang.org/x/time/rate"
)

var AnalyzeContractsCommand = Command(analyzeContractsE,
	"analyze-contracts <start_block> <end_block>",
	"Analyze blocks to extract contract types and their fields",
	ExactArgs(2),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("rpc-endpoint", "https://api.trongrid.io", "Tron RPC endpoint")
		flags.String("tron-api-key", "", "Tron API key for RPC access")
		flags.String("output", "contract-types.json", "Output file for contract types")
		flags.Duration("interval-between-fetch", 100*time.Millisecond, "Interval between block fetches (default: 100ms to stay under 15qps limit)")
		flags.Int("max-requests-per-second", 14, "Maximum requests per second to TronGrid API (default: 14 to stay under 15qps limit)")
	}),
)

type ContractType struct {
	Type       string                 `json:"type"`
	Fields     map[string]interface{} `json:"fields"`
	Count      int                    `json:"count"`
	Examples   []interface{}          `json:"examples"`
	MaxExample interface{}            `json:"max_example"`
}

func analyzeContractsE(cmd *cobra.Command, args []string) error {
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

	outputFile, _ := cmd.Flags().GetString("output")
	intervalBetweenFetch, _ := cmd.Flags().GetDuration("interval-between-fetch")
	maxRPS, _ := cmd.Flags().GetInt("max-requests-per-second")

	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(maxRPS), 1)

	ctx := context.Background()
	client := rpc.NewTronClient(rpcEndpoint, apiKey)

	contractTypes := make(map[string]*ContractType)

	for blockNum := startBlock; blockNum <= endBlock; blockNum++ {
		// Wait for rate limiter
		if err := limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter error: %w", err)
		}

		block, err := client.GetBlock(ctx, blockNum)
		if err != nil {
			return fmt.Errorf("failed to get block %d: %w", blockNum, err)
		}

		for _, tx := range block.Transactions {
			// Wait for rate limiter before fetching transaction info
			if err := limiter.Wait(ctx); err != nil {
				return fmt.Errorf("rate limiter error: %w", err)
			}

			txInfo, err := client.GetTransactionInfoByBlockNum(ctx, blockNum, tx.TxId)
			if err != nil {
				return fmt.Errorf("failed to get transaction info for tx %s: %w", tx.TxId, err)
			}

			for _, contract := range tx.RawData.Contract {
				contractType := contract.Type
				if _, exists := contractTypes[contractType]; !exists {
					contractTypes[contractType] = &ContractType{
						Type:     contractType,
						Fields:   make(map[string]interface{}),
						Examples: make([]interface{}, 0),
					}
				}

				// Extract fields from the contract parameter
				fields := extractFields(contract.Parameter.Value)

				// Add transaction info fields
				if txInfo != nil {
					// Add contract result fields
					if len(txInfo.ContractResult) > 0 {
						fields["contract_result"] = "array"
					}

					// Add internal transactions if any
					if len(txInfo.InternalTransactions) > 0 {
						fields["internal_transactions"] = "array"
					}

					// Add events if any
					if len(txInfo.Log) > 0 {
						fields["events"] = "array"
					}

					// Add fee information
					if txInfo.Fee != 0 {
						fields["fee"] = "number"
					}

					// Add receipt information
					if txInfo.Receipt != nil {
						fields["receipt"] = "object"
					}
				}

				contractTypes[contractType].Fields = mergeFields(contractTypes[contractType].Fields, fields)
				contractTypes[contractType].Count++

				// Create a more complete example including transaction info if available
				example := map[string]interface{}{
					"parameter": contract.Parameter.Value,
				}
				if txInfo != nil {
					example["result"] = txInfo.ContractResult
					example["internal_transactions"] = txInfo.InternalTransactions
					example["events"] = txInfo.Log
					example["fee"] = txInfo.Fee
					if txInfo.Receipt != nil {
						example["receipt"] = map[string]interface{}{
							"result":             txInfo.Receipt.Result,
							"energy_usage":       txInfo.Receipt.EnergyUsage,
							"energy_fee":         txInfo.Receipt.EnergyFee,
							"energy_usage_total": txInfo.Receipt.EnergyUsageTotal,
							"net_usage":          txInfo.Receipt.NetUsage,
							"net_fee":            txInfo.Receipt.NetFee,
						}
					}
				}

				// Only keep up to 3 examples per contract type
				if len(contractTypes[contractType].Examples) < 3 {
					contractTypes[contractType].Examples = append(contractTypes[contractType].Examples, example)
				}

				// Update max example if this is the first one or if it has more fields
				if contractTypes[contractType].MaxExample == nil || len(fields) > len(contractTypes[contractType].Fields) {
					contractTypes[contractType].MaxExample = example
				}
			}
		}

		// Sleep between fetches if interval is set
		if intervalBetweenFetch > 0 && blockNum < endBlock {
			time.Sleep(intervalBetweenFetch)
		}
	}

	// Convert map to slice for sorting
	contractTypeSlice := make([]*ContractType, 0, len(contractTypes))
	for _, ct := range contractTypes {
		contractTypeSlice = append(contractTypeSlice, ct)
	}

	// Sort by count
	sort.Slice(contractTypeSlice, func(i, j int) bool {
		return contractTypeSlice[i].Count > contractTypeSlice[j].Count
	})

	// Write results to file
	output, err := json.MarshalIndent(contractTypeSlice, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal contract types: %w", err)
	}

	if err := os.WriteFile(outputFile, output, 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("Analyzed %d blocks and found %d contract types. Results written to %s\n",
		endBlock-startBlock+1, len(contractTypes), outputFile)
	return nil
}

func extractFields(value interface{}) map[string]interface{} {
	fields := make(map[string]interface{})

	switch v := value.(type) {
	case map[string]interface{}:
		// Check if this is a contract parameter with base64 encoded data
		if data, ok := v["data"].(string); ok {
			// Decode base64 data
			decoded, err := base64.StdEncoding.DecodeString(data)
			if err == nil {
				// Try to unmarshal as JSON
				var jsonData map[string]interface{}
				if err := json.Unmarshal(decoded, &jsonData); err == nil {
					// Extract fields from the decoded JSON
					for key, val := range jsonData {
						fields[key] = getType(val)
					}
				}
			}
		}

		// Also extract any other fields directly from the map
		for key, val := range v {
			if key != "data" { // Skip the data field as we already processed it
				fields[key] = getType(val)
			}
		}
	case []interface{}:
		if len(v) > 0 {
			fields["items"] = getType(v[0])
		}
	}

	return fields
}

func getType(value interface{}) string {
	switch v := value.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func mergeFields(existing, new map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// Copy existing fields
	for k, v := range existing {
		result[k] = v
	}

	// Merge new fields
	for k, v := range new {
		if existingType, exists := result[k]; exists {
			// If types are different, mark as mixed
			if existingType != v {
				result[k] = "mixed"
			}
		} else {
			result[k] = v
		}
	}

	return result
}
