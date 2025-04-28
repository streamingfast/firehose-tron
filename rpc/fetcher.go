package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/cli"
	"github.com/streamingfast/dgrpc"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	"github.com/streamingfast/firehose-tron/tron/pb/api"
	"github.com/streamingfast/firehose-tron/tron/pb/core"
	"go.uber.org/zap"
	"google.golang.org/grpc"
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

	transactionInfoList, err := GetTransactionInfoByBlockNum(ctx, client, uint64(requestBlockNum))
	if err != nil {
		return nil, false, fmt.Errorf("getting transaction info: %w", err)
	}

	areTransactionsIntegral, err := verifyTransactionsIntegrity(blockExt.Transactions, transactionInfoList.TransactionInfo)
	if err != nil {
		return nil, false, fmt.Errorf("verifying transactions integrity: %w", err)
	}

	if !areTransactionsIntegral {
		return nil, false, fmt.Errorf("transactions are not integral")
	}

	// Convert block extension and transaction info list to our block type
	block, err := convertBlockAndTransactionsToBlock(blockExt, transactionInfoList)
	if err != nil {
		return nil, false, fmt.Errorf("converting block: %w", err)
	}

	// Convert to pbbstream.Block format
	return convertBlock(block)
}

func GetBlock(ctx context.Context, client api.WalletClient, blockNum int64) (*api.BlockExtention, error) {
	block, err := client.GetBlockByNum2(ctx, &api.NumberMessage{Num: blockNum})
	if err != nil {
		return nil, fmt.Errorf("get block: %w", err)
	}

	return block, nil
}

func GetTransactionInfoByBlockNum(ctx context.Context, client api.WalletClient, blockNum uint64) (*api.TransactionInfoList, error) {
	txInfoList, err := client.GetTransactionInfoByBlockNum(ctx, &api.NumberMessage{Num: int64(blockNum)})
	if err != nil {
		return nil, fmt.Errorf("get block: %w", err)
	}

	return txInfoList, nil
}

func (f *Fetcher) fetchLatestBlockNum(ctx context.Context, client api.WalletClient) (int64, error) {
	block, err := client.GetNowBlock2(ctx, &api.EmptyMessage{})
	if err != nil {
		return 0, fmt.Errorf("fetching latest block num: %w", err)
	}

	f.latestBlockNum = block.BlockHeader.RawData.Number
	return block.BlockHeader.RawData.Number, nil
}

func generateBlockTransactionsHash(getBlockTransactions []*api.TransactionExtention) ([]byte, error) {
	var txIDs []string
	for _, tx := range getBlockTransactions {
		txIDs = append(txIDs, hex.EncodeToString(tx.Txid))
	}

	h := sha256.New()
	for _, txID := range txIDs {
		if _, err := h.Write([]byte(txID)); err != nil {
			return nil, fmt.Errorf("write to hash: %w", err)
		}
	}

	return h.Sum(nil), nil
}

func generateTransactionInfoTransactionsHash(getTransactionTransactions []*core.TransactionInfo) ([]byte, error) {
	var txIDs []string
	for _, tx := range getTransactionTransactions {
		txIDs = append(txIDs, hex.EncodeToString(tx.Id))
	}

	h := sha256.New()
	for _, txID := range txIDs {
		if _, err := h.Write([]byte(txID)); err != nil {
			return nil, fmt.Errorf("write to hash: %w", err)
		}
	}

	return h.Sum(nil), nil
}

func verifyTransactionsIntegrity(getBlockTransactions []*api.TransactionExtention, getTransactionTransactions []*core.TransactionInfo) (bool, error) {
	blockTransactionsHash, err := generateBlockTransactionsHash(getBlockTransactions)
	if err != nil {
		return false, fmt.Errorf("generate block transactions hash: %w", err)
	}

	transactionInfoTransactionsHash, err := generateTransactionInfoTransactionsHash(getTransactionTransactions)
	if err != nil {
		return false, fmt.Errorf("generate transaction info transactions hash: %w", err)
	}

	return bytes.Equal(blockTransactionsHash, transactionInfoTransactionsHash), nil
}

func convertBlockAndTransactionsToBlock(blockExt *api.BlockExtention, transactionInfoList *api.TransactionInfoList) (*pbtron.Block, error) {
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
	for i, txExt := range blockExt.Transactions {
		tx, err := convertTransactionExtentionToTransaction(txExt)
		if err != nil {
			return nil, fmt.Errorf("failed to convert transaction: %w", err)
		}
		// TODO Remove this once typed is defined
		// We can safely assume that the transaction info list is in the same order as the transactions since we validated the integrity beforehand
		anyTxInfo, err := anypb.New(transactionInfoList.TransactionInfo[i])
		if err != nil {
			return nil, fmt.Errorf("failed to convert transaction extension to Any: %w", err)
		}
		tx.TransactionInfo = anyTxInfo
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
		RefBlockBytes: rawData.RefBlockBytes,
		RefBlockHash:  rawData.RefBlockHash,
		Expiration:    rawData.Expiration,
		Timestamp:     rawData.Timestamp,
	}

	// TODO Remove this once typed is defined
	anyTxExt, err := anypb.New(txExt)
	if err != nil {
		return nil, fmt.Errorf("failed to convert transaction extension to Any: %w", err)
	}
	tx.TransactionExtention = anyTxExt

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
			ourContract.Parameter = contract.Parameter
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

func convertBlock(block *pbtron.Block) (*pbbstream.Block, bool, error) {
	var parentBlockNum uint64
	if block.Header.Number > 0 {
		parentBlockNum = block.Header.Number - 1
	}
	// For now we use 20 blocks behind as libNum
	var libNum uint64
	if block.Header.Number > 20 {
		libNum = block.Header.Number - 20
	} else {
		libNum = 0
	}

	// Create a new Firehose block
	firehoseBlock := &pbbstream.Block{
		Id:        hex.EncodeToString(block.Id),
		Number:    block.Header.Number,
		ParentId:  hex.EncodeToString(block.Header.ParentHash),
		ParentNum: parentBlockNum,
		Timestamp: &timestamppb.Timestamp{
			Seconds: block.Header.Timestamp / 1000,
			Nanos:   int32((block.Header.Timestamp % 1000) * 1000000),
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
