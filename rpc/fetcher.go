package rpc

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/json"

	"github.com/mr-tron/base58"
	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/cli"
	"github.com/streamingfast/dgrpc"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	"github.com/streamingfast/firehose-tron/tron/pb/api"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ blockpoller.BlockFetcher[api.WalletClient] = (*Fetcher)(nil)

type Fetcher struct {
	clients                  *firecoreRPC.Clients[api.WalletClient]
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger
	latestBlockNum           int64
}

type apiKeyCredentials struct {
	apiKey string
}

func (c *apiKeyCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"TRON-PRO-API-KEY": c.apiKey,
	}, nil
}

func (c *apiKeyCredentials) RequireTransportSecurity() bool {
	return false
}

func NewTronClient(url string, apiKey string) api.WalletClient {
	conn, err := dgrpc.NewExternalClientConn(
		url,
		dgrpc.WithMustAutoTransportCredentials(false, true, false),
		grpc.WithPerRPCCredentials(&apiKeyCredentials{apiKey: apiKey}),
	)
	cli.NoError(err, "Failed to create client connection")

	return api.NewWalletClient(conn)
}

func NewFetcher(
	clients *firecoreRPC.Clients[api.WalletClient],
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger) *Fetcher {
	return &Fetcher{
		clients:                  clients,
		fetchInterval:            fetchInterval,
		latestBlockRetryInterval: latestBlockRetryInterval,
		logger:                   logger,
	}
}

func (f *Fetcher) IsBlockAvailable(blockNum uint64) bool {
	return uint64(f.latestBlockNum) >= blockNum
}

func (f *Fetcher) Fetch(ctx context.Context, client api.WalletClient, requestBlockNum uint64) (b *pbbstream.Block, skipped bool, err error) {
	f.logger.Info("fetching block", zap.Uint64("block_num", requestBlockNum))

	sleepDuration := time.Duration(0)
	for f.latestBlockNum < int64(requestBlockNum) {
		time.Sleep(sleepDuration)

		f.latestBlockNum, err = f.fetchLatestBlockNum(ctx, client)
		if err != nil {
			return nil, false, fmt.Errorf("fetching latest block num: %w", err)
		}

		f.logger.Info("got latest block num", zap.Int64("latest_block_num", f.latestBlockNum), zap.Uint64("requested_block_num", requestBlockNum))

		if f.latestBlockNum >= int64(requestBlockNum) {
			break
		}
		sleepDuration = f.latestBlockRetryInterval
	}

	// Fetch block data
	blockExt, err := GetBlock(ctx, client, int64(requestBlockNum))
	if err != nil {
		return nil, false, fmt.Errorf("getting block: %w", err)
	}

	// Convert block extension to our block type
	block, err := convertBlockExtentionToBlock(blockExt)
	if err != nil {
		return nil, false, fmt.Errorf("converting block: %w", err)
	}

	// Convert to pbbstream.Block format
	return convertBlock(block)
}

func GetBlock(ctx context.Context, client api.WalletClient, blockNum int64) (*api.BlockExtention, error) {
	// Create a context with timeout for the RPC call
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	block, err := client.GetBlockByNum2(ctx, &api.NumberMessage{Num: blockNum})
	if err != nil {
		return nil, fmt.Errorf("get block: %w", err)
	}

	return block, nil
}

func (f *Fetcher) fetchLatestBlockNum(ctx context.Context, client api.WalletClient) (int64, error) {
	block, err := client.GetNowBlock2(ctx, &api.EmptyMessage{})
	if err != nil {
		return 0, fmt.Errorf("fetching latest block num: %w", err)
	}

	f.latestBlockNum = block.BlockHeader.RawData.Number
	return block.BlockHeader.RawData.Number, nil
}

func convertBlockExtentionToBlock(blockExt *api.BlockExtention) (*pbtron.Block, error) {
	block := &pbtron.Block{
		Id: blockExt.Blockid,
		Header: &pbtron.BlockHeader{
			Number:           uint64(blockExt.BlockHeader.RawData.Number),
			TxTrieRoot:       blockExt.BlockHeader.RawData.TxTrieRoot,
			WitnessAddress:   blockExt.BlockHeader.RawData.WitnessAddress,
			ParentNumber:     uint64(blockExt.BlockHeader.RawData.Number - 1),
			ParentHash:       blockExt.BlockHeader.RawData.ParentHash,
			Version:          uint32(blockExt.BlockHeader.RawData.Version),
			Timestamp:        blockExt.BlockHeader.RawData.Timestamp,
			WitnessSignature: blockExt.BlockHeader.WitnessSignature,
		},
	}

	// Convert transactions
	for _, txExt := range blockExt.Transactions {
		tx, err := convertTransactionExtentionToTransaction(txExt)
		if err != nil {
			return nil, fmt.Errorf("failed to convert transaction: %w", err)
		}
		block.Transactions = append(block.Transactions, tx)
	}

	return block, nil
}

func convertTransactionExtentionToTransaction(txExt *api.TransactionExtention) (*pbtron.Transaction, error) {
	if txExt == nil || txExt.Transaction == nil {
		return nil, fmt.Errorf("transaction extension or transaction is nil")
	}

	// Get the raw transaction data
	rawData := txExt.Transaction.RawData
	if rawData == nil {
		return nil, fmt.Errorf("transaction raw data is nil")
	}

	// Create our flattened transaction
	tx := &pbtron.Transaction{
		Txid:          txExt.Txid,
		Signature:     txExt.Transaction.Signature,
		Result:        txExt.Result != nil && txExt.Result.Code == 0, // Success if code is 0
		ResultMessage: string(txExt.Result.GetMessage()),             // Convert bytes to string
		RefBlockBytes: rawData.RefBlockBytes,
		RefBlockHash:  rawData.RefBlockHash,
		Expiration:    rawData.Expiration,
		Timestamp:     rawData.Timestamp,
	}

	// Convert contracts
	for _, contract := range rawData.Contract {
		if contract == nil {
			continue
		}

		// Create our contract
		ourContract := &pbtron.Contract{
			Type: pbtron.Contract_ContractType(contract.Type),
		}

		// Convert parameter if it exists
		if contract.Parameter != nil {
			anyParam, err := anypb.New(contract.Parameter)
			if err != nil {
				return nil, fmt.Errorf("failed to convert contract parameter to any: %w", err)
			}
			ourContract.Parameter = anyParam
		}

		// Add provider and contract name if they exist
		if contract.Provider != nil {
			ourContract.Provider = contract.Provider
		}
		if contract.ContractName != nil {
			ourContract.ContractName = contract.ContractName
		}
		ourContract.PermissionId = contract.PermissionId

		tx.Contracts = append(tx.Contracts, ourContract)
	}

	return tx, nil
}

func hexToTronAddress(addr string) string {
	if len(addr) == 0 {
		return ""
	}

	// First decode from base64
	decoded, err := base64.StdEncoding.DecodeString(addr)
	if err != nil {
		fmt.Printf("Error decoding base64: %v\n", err)
		return addr
	}

	// The decoded bytes should be the raw address bytes
	// Add Tron address prefix (0x41) if not already present
	if len(decoded) > 0 && decoded[0] != 0x41 {
		decoded = append([]byte{0x41}, decoded...)
	}

	// Calculate checksum
	hash := sha256.Sum256(decoded)
	hash = sha256.Sum256(hash[:])
	checksum := hash[:4]

	// Append checksum
	addressWithChecksum := append(decoded, checksum...)

	// Base58 encode
	result := "T" + base58.Encode(addressWithChecksum)
	return result
}

func decodeContractParameter(param *anypb.Any) (map[string]interface{}, error) {
	paramMap := make(map[string]interface{})

	// Convert Any to map[string]interface{}
	paramBytes, err := protojson.Marshal(param)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal contract parameter: %w", err)
	}

	if err := json.Unmarshal(paramBytes, &paramMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal contract parameter: %w", err)
	}

	// Handle nested structures
	var processValue func(interface{}) interface{}
	processValue = func(value interface{}) interface{} {
		switch v := value.(type) {
		case string:
			// Check if it's a base64 encoded bytes field
			if _, err := base64.StdEncoding.DecodeString(v); err == nil {
				// If it's an address field (ends with "Address"), convert to Tron format
				return hexToTronAddress(v)
			}
			return v
		case []interface{}:
			// Handle arrays
			result := make([]interface{}, len(v))
			for i, item := range v {
				result[i] = processValue(item)
			}
			return result
		case map[string]interface{}:
			// Handle maps (nested objects)
			result := make(map[string]interface{})
			for key, val := range v {
				// Special handling for address fields in nested structures
				if strings.HasSuffix(key, "Address") {
					if addr, ok := val.(string); ok {
						result[key] = hexToTronAddress(addr)
						continue
					}
				}
				result[key] = processValue(val)
			}
			return result
		default:
			return v
		}
	}

	// Process the entire parameter map
	for key, value := range paramMap {
		paramMap[key] = processValue(value)
	}

	return paramMap, nil
}

func convertBlock(block *pbtron.Block) (*pbbstream.Block, bool, error) {
	var parentBlockNum uint64
	if block.Header.Number > 0 {
		parentBlockNum = block.Header.Number - 1
	}
	// TODO: Last irreversible block number
	// For now we can do this by subtracting 20 from the block number
	var libNum uint64
	if block.Header.Number > 20 {
		libNum = block.Header.Number - 20
	} else {
		libNum = 0
	}

	blockID, err := base64.StdEncoding.DecodeString(string(block.Id))
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode block id: %w", err)
	}

	parentHash, err := base64.StdEncoding.DecodeString(string(block.Header.ParentHash))
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode parent hash: %w", err)
	}

	// Create a new Firehose block
	firehoseBlock := &pbbstream.Block{
		Id:        hex.EncodeToString(blockID),
		Number:    block.Header.Number,
		ParentId:  hex.EncodeToString(parentHash),
		ParentNum: parentBlockNum,
		Timestamp: &timestamppb.Timestamp{
			Seconds: block.Header.Timestamp / 1000,
			Nanos:   int32((block.Header.Timestamp % 1000) * 1000000),
		},
		LibNum: libNum,
	}

	// Process transactions to decode addresses
	for _, tx := range block.Transactions {
		if tx.Contracts != nil {
			for _, contract := range tx.Contracts {
				if contract.Parameter != nil {
					decodedParam, err := decodeContractParameter(contract.Parameter)
					if err == nil {
						// Convert the decoded map back to JSON bytes
						jsonBytes, err := json.Marshal(decodedParam)
						if err == nil {
							// Update the contract parameter with decoded addresses
							contract.Parameter = &anypb.Any{
								TypeUrl: contract.Parameter.TypeUrl,
								Value:   jsonBytes,
							}
						}
					}
				}
			}
		}
	}

	// Create anypb payload with our block data
	anyBlock, err := anypb.New(block)
	if err != nil {
		return nil, false, fmt.Errorf("unable to create anypb: %w", err)
	}
	firehoseBlock.Payload = anyBlock

	return firehoseBlock, false, nil
}
