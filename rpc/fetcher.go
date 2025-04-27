package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"crypto/sha256"

	"encoding/hex"

	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/firehose-core/blockpoller"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"
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

	// Verify integrity between block and transaction info
	integrity, err := VerifyIntegrity(block, transactions)
	if err != nil {
		return nil, false, fmt.Errorf("verifying integrity: %w", err)
	}
	if !integrity {
		return nil, false, fmt.Errorf("integrity check failed: block and transaction info hashes do not match")
	}

	// Convert to pbbstream.Block format
	return convertBlock(block, transactions)
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
	reqBody := map[string]interface{}{
		"num": blockNum,
	}

	body, err := doRequest(c, ctx, "POST", "walletsolidity/gettransactioninfobyblocknum", reqBody)
	if err != nil {
		return nil, fmt.Errorf("get transaction info: %w", err)
	}

	return convertTransactionInfoFromJSON(body, txID)
}

// VerifyIntegrity checks if the block and transaction info hashes match
func VerifyIntegrity(block *pbtron.Block, txInfos []*pbtron.TransactionInfo) (bool, error) {
	blockHash, err := generateBlockHash(block)
	if err != nil {
		return false, fmt.Errorf("generate block hash: %w", err)
	}

	txInfoHash, err := generateTransactionInfoHash(txInfos)
	if err != nil {
		return false, fmt.Errorf("generate transaction info hash: %w", err)
	}

	return bytes.Equal(blockHash, txInfoHash), nil
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

// convertTransactionInfoFromJSON converts a JSON response to a TransactionInfo
func convertTransactionInfoFromJSON(data []byte, txID string) (*pbtron.TransactionInfo, error) {
	var txInfos []struct {
		Log []struct {
			Address string   `json:"address"`
			Data    string   `json:"data"`
			Topics  []string `json:"topics"`
		} `json:"log"`
		BlockNumber    uint64   `json:"blockNumber"`
		ContractResult []string `json:"contractResult"`
		BlockTimeStamp int64    `json:"blockTimeStamp"`
		Receipt        struct {
			Result           string `json:"result"`
			EnergyUsage      uint64 `json:"energy_usage"`
			EnergyUsageTotal uint64 `json:"energy_usage_total"`
			NetUsage         uint64 `json:"net_usage"`
			EnergyFee        uint64 `json:"energy_fee"`
			NetFee           uint64 `json:"net_fee"`
		} `json:"receipt"`
		ID                   string `json:"id"`
		ContractAddress      string `json:"contract_address"`
		Fee                  uint64 `json:"fee"`
		InternalTransactions []struct {
			CallerAddress     string `json:"caller_address"`
			Note              string `json:"note"`
			TransferToAddress string `json:"transferTo_address"`
			CallValueInfo     []struct {
				CallValue uint64 `json:"callValue"`
			} `json:"callValueInfo"`
			Hash string `json:"hash"`
		} `json:"internal_transactions"`
	}

	if err := json.Unmarshal(data, &txInfos); err != nil {
		return nil, fmt.Errorf("unmarshal transaction info: %w", err)
	}

	// Find the transaction info with matching ID
	for _, txInfo := range txInfos {
		if txInfo.ID == txID {
			// Convert to protobuf TransactionInfo
			protoTxInfo := &pbtron.TransactionInfo{
				Id:  txInfo.ID,
				Fee: int64(txInfo.Fee),
				Receipt: &pbtron.Receipt{
					Result:           txInfo.Receipt.Result,
					EnergyUsage:      int64(txInfo.Receipt.EnergyUsage),
					EnergyUsageTotal: int64(txInfo.Receipt.EnergyUsageTotal),
					NetUsage:         int64(txInfo.Receipt.NetUsage),
					EnergyFee:        int64(txInfo.Receipt.EnergyFee),
					NetFee:           int64(txInfo.Receipt.NetFee),
				},
			}

			// Add logs if any
			for _, log := range txInfo.Log {
				address, err := hex.DecodeString(log.Address)
				if err != nil {
					return nil, fmt.Errorf("decode log address: %w", err)
				}
				data, err := hex.DecodeString(log.Data)
				if err != nil {
					return nil, fmt.Errorf("decode log data: %w", err)
				}
				topics := make([][]byte, len(log.Topics))
				for i, topic := range log.Topics {
					topicBytes, err := hex.DecodeString(topic)
					if err != nil {
						return nil, fmt.Errorf("decode log topic: %w", err)
					}
					topics[i] = topicBytes
				}
				protoTxInfo.Log = append(protoTxInfo.Log, &pbtron.Log{
					Address: address,
					Data:    data,
					Topics:  topics,
				})
			}

			// Add internal transactions if any
			for _, internalTx := range txInfo.InternalTransactions {
				callerAddress, err := hex.DecodeString(internalTx.CallerAddress)
				if err != nil {
					return nil, fmt.Errorf("decode caller address: %w", err)
				}
				transferToAddress, err := hex.DecodeString(internalTx.TransferToAddress)
				if err != nil {
					return nil, fmt.Errorf("decode transfer to address: %w", err)
				}
				hash, err := hex.DecodeString(internalTx.Hash)
				if err != nil {
					return nil, fmt.Errorf("decode hash: %w", err)
				}

				protoInternalTx := &pbtron.InternalTransaction{
					CallerAddress:     callerAddress,
					Note:              []byte(internalTx.Note),
					TransferToAddress: transferToAddress,
					Hash:              hash,
				}

				if len(internalTx.CallValueInfo) > 0 {
					protoInternalTx.CallValueInfo = []*pbtron.CallValueInfo{
						{
							CallValue: int64(internalTx.CallValueInfo[0].CallValue),
						},
					}
				}

				protoTxInfo.InternalTransactions = append(protoTxInfo.InternalTransactions, protoInternalTx)
			}

			// Add contract address if present
			if txInfo.ContractAddress != "" {
				contractAddress, err := hex.DecodeString(txInfo.ContractAddress)
				if err != nil {
					return nil, fmt.Errorf("decode contract address: %w", err)
				}
				protoTxInfo.ContractAddress = contractAddress
			}

			return protoTxInfo, nil
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
		tx, err := convertBlockTransactionFromJSON(txData)
		if err != nil {
			return nil, fmt.Errorf("convert transaction: %w", err)
		}
		block.Transactions = append(block.Transactions, tx)
	}

	return block, nil
}

// convertBlockTransactionFromJSON converts a JSON response to a Transaction
func convertBlockTransactionFromJSON(data []byte) (*pbtron.Transaction, error) {
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

	var parentBlockNum uint64
	if block.BlockHeader.RawData.Number > 0 {
		parentBlockNum = block.BlockHeader.RawData.Number - 1
	}
	// TODO: Last irreversible block number
	// For now we can do this by subtracting 20 from the block number
	libNum := parentBlockNum
	// Create a new Firehose block
	firehoseBlock := &pbbstream.Block{
		Id:        block.BlockId,
		Number:    block.BlockHeader.RawData.Number,
		ParentId:  string(block.BlockHeader.RawData.ParentHash),
		ParentNum: parentBlockNum,
		Timestamp: &timestamppb.Timestamp{
			Seconds: block.BlockHeader.RawData.Timestamp / 1000,
			Nanos:   int32((block.BlockHeader.RawData.Timestamp % 1000) * 1000000),
		},
		LibNum: libNum,
	}

	// Create anypb payload with our block data
	anyBlock, err := anypb.New(block)
	if err != nil {
		return nil, false, fmt.Errorf("unable to create anypb: %w", err)
	}
	firehoseBlock.Payload = anyBlock

	return firehoseBlock, false, nil
}

// generateBlockHash generates a hash from the transaction IDs in a block
func generateBlockHash(block *pbtron.Block) ([]byte, error) {
	var txIDs []string
	for _, tx := range block.Transactions {
		txIDs = append(txIDs, tx.TxId)
	}

	h := sha256.New()
	for _, txID := range txIDs {
		if _, err := h.Write([]byte(txID)); err != nil {
			return nil, fmt.Errorf("write to hash: %w", err)
		}
	}

	return h.Sum(nil), nil
}

// generateTransactionInfoHash generates a hash from the transaction IDs in transaction info
func generateTransactionInfoHash(txInfos []*pbtron.TransactionInfo) ([]byte, error) {
	var txIDs []string
	for _, txInfo := range txInfos {
		txIDs = append(txIDs, txInfo.Id)
	}

	h := sha256.New()
	for _, txID := range txIDs {
		if _, err := h.Write([]byte(txID)); err != nil {
			return nil, fmt.Errorf("write to hash: %w", err)
		}
	}

	return h.Sum(nil), nil
}
