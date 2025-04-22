package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
}

func NewTronClient(url string) *TronClient {
	return &TronClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		url: url,
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
	url := fmt.Sprintf("%s/walletsolidity/getnowblock", client.url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("accept", "application/json")

	resp, err := client.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var response struct {
		BlockHeader struct {
			RawData struct {
				Number uint64 `json:"number"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	f.latestBlockNum = response.BlockHeader.RawData.Number
	return response.BlockHeader.RawData.Number, nil
}

func (c *TronClient) GetBlock(ctx context.Context, blockNum uint64) (*pbtron.Block, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "wallet/getblockbynum",
		"params":  []interface{}{blockNum},
		"id":      1,
	}

	var resp struct {
		Result *pbtron.Block `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := c.doRequest(ctx, reqBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to get block: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", resp.Error.Message)
	}

	return resp.Result, nil
}

func (c *TronClient) GetTransactionInfoByBlockNum(ctx context.Context, blockNum uint64, txID string) (*pbtron.TransactionInfo, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "wallet/gettransactioninfobyblocknum",
		"params":  []interface{}{blockNum},
		"id":      1,
	}

	var resp struct {
		Result []*pbtron.TransactionInfo `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := c.doRequest(ctx, reqBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to get transaction info: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", resp.Error.Message)
	}

	// Find the transaction info with matching ID
	for _, txInfo := range resp.Result {
		if txInfo.Id == txID {
			return txInfo, nil
		}
	}

	return nil, fmt.Errorf("transaction info not found for tx %s", txID)
}

func (c *TronClient) doRequest(ctx context.Context, reqBody interface{}, resp interface{}) error {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(resp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
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

	// Add transactions to the block
	for i, tx := range block.Transactions {
		txInfo := transactions[i]
		if txInfo == nil {
			continue
		}

		// Create transaction trace
		trace := &pbbstream.TransactionTrace{
			Id:     tx.TxId,
			Status: pbbstream.TransactionStatus_TRANSACTIONSTATUS_EXECUTED,
			Receipt: &pbbstream.TransactionReceipt{
				Status:   txInfo.Receipt.Result == "SUCCESS",
				GasUsed:  uint64(txInfo.Receipt.EnergyUsage),
				GasPrice: uint64(txInfo.Fee / txInfo.Receipt.EnergyUsage),
			},
		}

		// Add internal transactions if any
		for _, internalTx := range txInfo.InternalTransactions {
			internalTrace := &pbbstream.TransactionTrace{
				Id:     string(internalTx.Hash), // Convert bytes to string
				Status: pbbstream.TransactionStatus_TRANSACTIONSTATUS_EXECUTED,
			}
			trace.InternalTraces = append(trace.InternalTraces, internalTrace)
		}

		firehoseBlock.TransactionTraces = append(firehoseBlock.TransactionTraces, trace)
	}

	return firehoseBlock, false, nil
}
