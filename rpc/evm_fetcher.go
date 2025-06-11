package rpc

import (
	"context"
	"fmt"
	"time"

	"github.com/streamingfast/bstream"
	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/eth-go"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/firehose-core/blockpoller"
	"github.com/streamingfast/firehose-core/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"github.com/streamingfast/firehose-ethereum/block"
	"github.com/streamingfast/firehose-ethereum/blockfetcher"
	pbeth "github.com/streamingfast/firehose-ethereum/types/pb/sf/ethereum/type/v2"
	pbtron "github.com/streamingfast/firehose-tron/pb/sf/tron/type/v1"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	pbtroncore "github.com/streamingfast/tron-protocol/pb/core"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ blockpoller.BlockFetcher[*ethRPC.Client] = (*EVMFetcher)(nil)

type EVMFetcher struct {
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger

	tronClients *firecoreRPC.Clients[pbtronapi.WalletClient]
	tronFetcher *Fetcher
	evmFetcher  *blockfetcher.BlockFetcher
}

func NewEVMFetcher(
	tronClients *firecoreRPC.Clients[pbtronapi.WalletClient],
	tronFetcher *Fetcher,
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger,
) *EVMFetcher {

	evmFetcher := blockfetcher.NewBlockFetcher(fetchInterval, latestBlockRetryInterval, 0, block.RpcToEthBlock, logger)
	evmFetcher.SkipReceipts(true) // we set 0 parallel transaction fetchers and skip receipts, so that it uses getLogs() instead

	return &EVMFetcher{
		fetchInterval:            fetchInterval,
		latestBlockRetryInterval: latestBlockRetryInterval,
		logger:                   logger,
		tronClients:              tronClients,
		tronFetcher:              tronFetcher,
		evmFetcher:               evmFetcher,
	}
}

func (f *EVMFetcher) IsBlockAvailable(blockNum uint64) bool {
	return f.evmFetcher.IsBlockAvailable(blockNum)
}

func (f *EVMFetcher) Fetch(ctx context.Context, client *ethRPC.Client, requestBlockNum uint64) (b *pbbstream.Block, skipped bool, err error) {
	f.logger.Debug("test logger")

	block, err := f.evmFetcher.FetchPBEth(ctx, client, requestBlockNum)
	if err != nil {
		return nil, false, err
	}

	tronBlock, err := rpc.WithClientsContext(f.tronClients, ctx,
		func(ctx context.Context, client pbtronapi.WalletClient) (*pbtron.Block, error) {
			out, err := f.tronFetcher.fetch(ctx, client, requestBlockNum)
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	)
	if err != nil {
		return nil, false, err
	}

	if requestBlockNum != 0 {
		tronTransactions := make(map[string]*pbtron.Transaction)
		for _, trx := range tronBlock.Transactions {
			tronTransactions[eth.Hash(trx.Info.Id).String()] = trx
		}
		block.Header.LogsBloom = nil // unused in Tron
		cumulativeGasUsed := uint64(0)
		for _, trx := range block.TransactionTraces {
			tronTrx := tronTransactions[eth.Hash(trx.Hash).String()]
			if tronTrx == nil {
				panic(fmt.Sprintf("trx %q not found in tron block: this shouldn't happen", eth.Hash(trx.Hash).String()))
			}
			trx.GasUsed = uint64(tronTrx.Info.Receipt.EnergyUsageTotal)
			cumulativeGasUsed += trx.GasUsed
			trx.Receipt.CumulativeGasUsed = cumulativeGasUsed

			switch tronTrx.Info.Result {
			case pbtroncore.TransactionInfo_SUCESS:
				trx.Status = 1
			case pbtroncore.TransactionInfo_FAILED:
				trx.Status = 2
			default:
				panic("unsupported trx status")
			}
		}
	}

	anyBlock, err := anypb.New(block)
	if err != nil {
		return nil, false, fmt.Errorf("create any block: %w", err)
	}

	return &pbbstream.Block{
		Number:    block.Number,
		Id:        block.GetFirehoseBlockID(),
		ParentId:  block.GetFirehoseBlockParentID(),
		Timestamp: timestamppb.New(block.GetFirehoseBlockTime()),
		LibNum:    ethBlockLIBNum(block),
		ParentNum: block.GetFirehoseBlockParentNumber(),
		Payload:   anyBlock,
	}, false, nil
}

func ethBlockLIBNum(b *pbeth.Block) uint64 {
	if b.Number <= bstream.GetProtocolFirstStreamableBlock+200 {
		return bstream.GetProtocolFirstStreamableBlock
	}

	return b.Number - 200
}
