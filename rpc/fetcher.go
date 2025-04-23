package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"encoding/hex"

	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/firehose-core/blockpoller"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ blockpoller.BlockFetcher[*TronClient] = (*Fetcher)(nil)

type Fetcher struct {
	client                   *TronClient
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger
	latestBlockNum           uint64
}

type TronClient struct {
	client *http.Client
	url    string
	apiKey string
}

func NewTronClient(url string, apiKey string) *TronClient {
	return &TronClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		url:    url,
		apiKey: apiKey,
	}
}

func NewFetcher(
	client *TronClient,
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger) *Fetcher {
	return &Fetcher{
		client:                   client,
		fetchInterval:            fetchInterval,
		latestBlockRetryInterval: latestBlockRetryInterval,
		logger:                   logger,
	}
}

func (f *Fetcher) IsBlockAvailable(blockNum uint64) bool {
	return blockNum <= f.latestBlockNum
}

func (f *Fetcher) Fetch(ctx context.Context, client *TronClient, requestBlockNum uint64) (b *pbbstream.Block, skipped bool, err error) {
	f.logger.Info("fetching block", zap.Uint64("block_num", requestBlockNum))

	sleepDuration := time.Duration(0)
	for f.latestBlockNum < requestBlockNum {
		time.Sleep(sleepDuration)

		f.latestBlockNum, err = f.fetchLatestBlockNum(ctx, client)
		if err != nil {
			return nil, false, fmt.Errorf("fetching latest block num: %w", err)
		}

		f.logger.Info("got latest block num", zap.Uint64("latest_block_num", f.latestBlockNum), zap.Uint64("requested_block_num", requestBlockNum))

		if f.latestBlockNum >= requestBlockNum {
			break
		}
		sleepDuration = f.latestBlockRetryInterval
	}

	// Fetch block data
	block, err := client.GetBlock(ctx, requestBlockNum)
	if err != nil {
		return nil, false, fmt.Errorf("getting block: %w", err)
	}

	// Fetch transaction info for all transactions in the block
	transactions := make([]*pbtron.TransactionInfo, len(block.Transactions))
	for i, tx := range block.Transactions {
		txInfo, err := client.GetTransactionInfoByBlockNum(ctx, requestBlockNum, tx.TxId)
		if err != nil {
			return nil, false, fmt.Errorf("getting transaction info for tx %s: %w", tx.TxId, err)
		}
		transactions[i] = txInfo
	}

	// Convert to pbbstream.Block format
	return convertBlock(block, transactions)
}

func (f *Fetcher) fetchLatestBlockNum(ctx context.Context, client *TronClient) (uint64, error) {
	body, err := doRequest(client, ctx, "GET", "walletsolidity/getnowblock", nil)
	if err != nil {
		return 0, fmt.Errorf("fetching latest block num: %w", err)
	}

	block, err := convertBlockFromJSON(body)
	if err != nil {
		return 0, fmt.Errorf("converting block: %w", err)
	}

	f.latestBlockNum = block.BlockHeader.RawData.Number
	return block.BlockHeader.RawData.Number, nil
}

func (c *TronClient) GetBlock(ctx context.Context, blockNum uint64) (*pbtron.Block, error) {
	reqBody := map[string]interface{}{
		"detail":    true,
		"id_or_num": fmt.Sprintf("%d", blockNum),
	}

	body, err := doRequest(c, ctx, "POST", "walletsolidity/getblock", reqBody)
	if err != nil {
		return nil, fmt.Errorf("get block: %w", err)
	}

	return convertBlockFromJSON(body)
}

func (c *TronClient) GetTransactionInfoByBlockNum(ctx context.Context, blockNum uint64, txID string) (*pbtron.TransactionInfo, error) {
	body, err := doRequest(c, ctx, "GET", "walletsolidity/gettransactioninfobyblocknum", map[string]string{
		"num": fmt.Sprintf("%d", blockNum),
	})
	if err != nil {
		return nil, fmt.Errorf("get transaction info: %w", err)
	}

	// Parse the response as an array of transaction info
	var txInfos []*pbtron.TransactionInfo
	if err := json.Unmarshal(body, &txInfos); err != nil {
		return nil, fmt.Errorf("unmarshal transaction info: %w", err)
	}

	// Find the transaction info with matching ID
	for _, txInfo := range txInfos {
		if txInfo.Id == txID {
			return txInfo, nil
		}
	}

	return nil, fmt.Errorf("transaction info not found for tx %s", txID)
}

// convertBlockFromJSON converts a JSON response to a Block
func convertBlockFromJSON(data []byte) (*pbtron.Block, error) {
	var jsonBlock struct {
		BlockID     string `json:"blockID"`
		BlockHeader struct {
			RawData struct {
				Number         uint64 `json:"number"`
				TxTrieRoot     string `json:"txTrieRoot"`
				WitnessAddress string `json:"witness_address"`
				ParentHash     string `json:"parentHash"`
				Version        uint32 `json:"version"`
				Timestamp      int64  `json:"timestamp"`
			} `json:"raw_data"`
			WitnessSignature string `json:"witness_signature"`
		} `json:"block_header"`
		Transactions []json.RawMessage `json:"transactions"`
	}

	if err := json.Unmarshal(data, &jsonBlock); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}

	block := &pbtron.Block{
		BlockId: jsonBlock.BlockID,
		BlockHeader: &pbtron.BlockHeader{
			RawData: &pbtron.RawData{
				Number:    jsonBlock.BlockHeader.RawData.Number,
				Version:   jsonBlock.BlockHeader.RawData.Version,
				Timestamp: jsonBlock.BlockHeader.RawData.Timestamp,
			},
		},
	}

	// Convert hex strings to bytes
	if jsonBlock.BlockHeader.RawData.TxTrieRoot != "" {
		txTrieRoot, err := hex.DecodeString(jsonBlock.BlockHeader.RawData.TxTrieRoot)
		if err != nil {
			return nil, fmt.Errorf("decode tx trie root: %w", err)
		}
		block.BlockHeader.RawData.TxTrieRoot = txTrieRoot
	}

	if jsonBlock.BlockHeader.RawData.WitnessAddress != "" {
		witnessAddress, err := hex.DecodeString(jsonBlock.BlockHeader.RawData.WitnessAddress)
		if err != nil {
			return nil, fmt.Errorf("decode witness address: %w", err)
		}
		block.BlockHeader.RawData.WitnessAddress = witnessAddress
	}

	if jsonBlock.BlockHeader.RawData.ParentHash != "" {
		parentHash, err := hex.DecodeString(jsonBlock.BlockHeader.RawData.ParentHash)
		if err != nil {
			return nil, fmt.Errorf("decode parent hash: %w", err)
		}
		block.BlockHeader.RawData.ParentHash = parentHash
	}

	if jsonBlock.BlockHeader.WitnessSignature != "" {
		witnessSignature, err := hex.DecodeString(jsonBlock.BlockHeader.WitnessSignature)
		if err != nil {
			return nil, fmt.Errorf("decode witness signature: %w", err)
		}
		block.BlockHeader.WitnessSignature = witnessSignature
	}

	// Convert transactions
	for _, txData := range jsonBlock.Transactions {
		tx, err := convertTransactionFromJSON(txData)
		if err != nil {
			return nil, fmt.Errorf("convert transaction: %w", err)
		}
		block.Transactions = append(block.Transactions, tx)
	}

	return block, nil
}

// convertTransactionFromJSON converts a JSON response to a Transaction
func convertTransactionFromJSON(data []byte) (*pbtron.Transaction, error) {
	var aux struct {
		Signature  []string `json:"signature"`
		TxId       string   `json:"txID"`
		RawDataHex string   `json:"raw_data_hex"`
		RawData    struct {
			Contract []struct {
				Type      string `json:"type"`
				Parameter struct {
					TypeUrl string      `json:"type_url"`
					Value   interface{} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
			RefBlockBytes string `json:"ref_block_bytes"`
			RefBlockHash  string `json:"ref_block_hash"`
			Expiration    int64  `json:"expiration"`
			Timestamp     int64  `json:"timestamp"`
		} `json:"raw_data"`
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return nil, fmt.Errorf("unmarshal transaction: %w", err)
	}

	tx := &pbtron.Transaction{
		TxId: aux.TxId,
	}

	// Convert signatures
	for _, sig := range aux.Signature {
		sigBytes, err := hex.DecodeString(sig)
		if err != nil {
			return nil, fmt.Errorf("decode signature: %w", err)
		}
		tx.Signature = append(tx.Signature, sigBytes)
	}

	// Convert raw data hex
	if aux.RawDataHex != "" {
		rawData, err := hex.DecodeString(aux.RawDataHex)
		if err != nil {
			return nil, fmt.Errorf("decode raw data hex: %w", err)
		}
		tx.RawDataHex = rawData
	}

	// Set raw data fields
	if len(aux.RawData.Contract) > 0 {
		tx.RawData = &pbtron.RawTransactionData{
			Contract: make([]*pbtron.Contract, len(aux.RawData.Contract)),
		}

		for i, contract := range aux.RawData.Contract {
			// Convert contract parameter value to bytes
			var valueBytes []byte
			switch v := contract.Parameter.Value.(type) {
			case string:
				valueBytes = []byte(v)
			case []byte:
				valueBytes = v
			default:
				// Try to marshal the value to JSON and then convert to bytes
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("marshal contract parameter value: %w", err)
				}
				valueBytes = jsonBytes
			}

			tx.RawData.Contract[i] = &pbtron.Contract{
				Type: contract.Type,
				Parameter: &pbtron.ContractParameter{
					TypeUrl: contract.Parameter.TypeUrl,
					Value: &pbtron.ContractValue{
						Data: valueBytes,
					},
				},
			}
		}

		if aux.RawData.RefBlockBytes != "" {
			refBlockBytes, err := hex.DecodeString(aux.RawData.RefBlockBytes)
			if err != nil {
				return nil, fmt.Errorf("decode ref block bytes: %w", err)
			}
			tx.RawData.RefBlockBytes = refBlockBytes
		}

		if aux.RawData.RefBlockHash != "" {
			refBlockHash, err := hex.DecodeString(aux.RawData.RefBlockHash)
			if err != nil {
				return nil, fmt.Errorf("decode ref block hash: %w", err)
			}
			tx.RawData.RefBlockHash = refBlockHash
		}

		tx.RawData.Expiration = aux.RawData.Expiration
		tx.RawData.Timestamp = aux.RawData.Timestamp
	}

	return tx, nil
}

// doRequest performs an HTTP request and returns the raw response body
func doRequest(client *TronClient, ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	// Build URL
	url := fmt.Sprintf("%s/%s", client.url, path)

	// Marshal request body if provided
	var reqBody []byte
	var err error
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	if client.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", client.apiKey)
	}

	// Send request
	res, err := client.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer res.Body.Close()

	// Read response
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", res.StatusCode, string(bodyBytes))
	}

	return bodyBytes, nil
}

func convertBlock(block *pbtron.Block, transactions []*pbtron.TransactionInfo) (*pbbstream.Block, bool, error) {
	// Create a new Firehose block
	firehoseBlock := &pbbstream.Block{
		Id:       block.BlockId,
		Number:   block.BlockHeader.RawData.Number,
		ParentId: string(block.BlockHeader.RawData.ParentHash), // Convert bytes to string
		Timestamp: &timestamppb.Timestamp{
			Seconds: block.BlockHeader.RawData.Timestamp / 1000,
			Nanos:   int32((block.BlockHeader.RawData.Timestamp % 1000) * 1000000),
		},
		LibNum: block.BlockHeader.RawData.Number, // Using current block as LIB for now
	}

	// // Add transactions to the block
	// for i, tx := range block.Transactions {
	// 	txInfo := transactions[i]
	// 	if txInfo == nil {
	// 		continue
	// 	}

	// 	// Create transaction trace
	// 	trace := &pbbstream.TransactionTrace{
	// 		Id:     tx.TxId,
	// 		Status: pbbstream.TransactionStatus_TRANSACTIONSTATUS_EXECUTED,
	// 		Receipt: &pbbstream.TransactionReceipt{
	// 			Status:   txInfo.Receipt.Result == "SUCCESS",
	// 			GasUsed:  uint64(txInfo.Receipt.EnergyUsage),
	// 			GasPrice: uint64(txInfo.Fee / txInfo.Receipt.EnergyUsage),
	// 		},
	// 	}

	// 	// Add internal transactions if any
	// 	for _, internalTx := range txInfo.InternalTransactions {
	// 		internalTrace := &pbbstream.TransactionTrace{
	// 			Id:     string(internalTx.Hash), // Convert bytes to string
	// 			Status: pbbstream.TransactionStatus_TRANSACTIONSTATUS_EXECUTED,
	// 		}
	// 		trace.InternalTraces = append(trace.InternalTraces, internalTrace)
	// 	}

	// 	firehoseBlock.TransactionTraces = append(firehoseBlock.TransactionTraces, trace)
	// }

	return firehoseBlock, false, nil
}
