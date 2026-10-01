package rpc

import (
	"context"
	"fmt"
	"sync"
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
	logger *zap.Logger

	tronClients *firecoreRPC.Clients[pbtronapi.WalletClient]
	tronFetcher *Fetcher
	evmHead     headWaiter

	fetchInterval time.Duration
	lastFetchLock sync.Mutex
	lastFetchAt   time.Time

	// One throttle per side so a failing EVM provider never masks a Tron
	// failure, and vice versa.
	evmFailures  *failureLogger
	tronFailures *failureLogger
}

func NewEVMFetcher(
	tronClients *firecoreRPC.Clients[pbtronapi.WalletClient],
	tronFetcher *Fetcher,
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger,
) *EVMFetcher {
	return &EVMFetcher{
		logger:        logger,
		tronClients:   tronClients,
		tronFetcher:   tronFetcher,
		evmHead:       headWaiter{retryInterval: latestBlockRetryInterval},
		fetchInterval: fetchInterval,
		evmFailures:   newFailureLogger(logger, failureLogInterval),
		tronFailures:  newFailureLogger(logger, failureLogInterval),
	}
}

func (f *EVMFetcher) IsBlockAvailable(blockNum uint64) bool {
	return uint64(f.evmHead.Latest()) >= blockNum
}

// Fetch retrieves requestBlockNum from both the EVM (JSON-RPC) and the Tron
// (gRPC) side and merges them into a single Firehose block.
//
// Failures are logged here on top of being returned: the block poller retries
// a failed fetch forever without logging anything, so a permanently failing
// endpoint (wrong API key, exhausted rate limit) would otherwise show up as a
// poller silently frozen on one block. A failure that keeps repeating is
// collapsed to one line every failureLogInterval.
func (f *EVMFetcher) Fetch(ctx context.Context, client *ethRPC.Client, requestBlockNum uint64) (b *pbbstream.Block, skipped bool, err error) {
	budget := callBudget(ctx)
	_, err = f.evmHead.waitFor(ctx, int64(requestBlockNum), budget, func(ctx context.Context) (int64, error) {
		head, err := client.LatestBlockNum(ctx)
		return int64(head), err
	})
	if err != nil {
		f.evmFailures.log("failed to fetch block from EVM endpoint, the poller will retry", err, zap.Uint64("block_num", requestBlockNum))
		return nil, false, err
	}

	f.throttle()

	evmCtx, cancel := withBudget(ctx, budget)
	defer cancel()

	block, err := f.fetchEVMBlock(evmCtx, client, requestBlockNum)
	if err != nil {
		f.evmFailures.log("failed to fetch block from EVM endpoint, the poller will retry", err, zap.Uint64("block_num", requestBlockNum))
		return nil, false, err
	}
	f.evmFailures.reset()

	tronBlock, err := f.fetchTronBlock(ctx, requestBlockNum)
	if err != nil {
		f.tronFailures.log("failed to fetch block from Tron endpoint, the poller will retry", err, zap.Uint64("block_num", requestBlockNum))
		return nil, false, err
	}
	f.tronFailures.reset()

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

// fetchEVMBlock is firehose-ethereum's blockfetcher.FetchPBEth without its head
// wait and throttle, with receipts skipped so logs come from one eth_getLogs.
// FetchPBEth keeps a head cache of its own and waits on it under the caller's
// deadline, which is what headWaiter exists to avoid.
func (f *EVMFetcher) fetchEVMBlock(ctx context.Context, client *ethRPC.Client, blockNum uint64) (*pbeth.Block, error) {
	rpcBlock, err := client.GetBlockByNumber(ctx, ethRPC.BlockNumber(blockNum), ethRPC.WithGetBlockFullTransaction())
	if err != nil {
		return nil, fmt.Errorf("fetching block %d: %w", blockNum, err)
	}

	// A load-balanced endpoint can answer null for a block its eth_blockNumber
	// already covered, when the request lands on a backend that lags behind.
	if rpcBlock == nil {
		return nil, fmt.Errorf("block %d not found on this endpoint, it is either not available yet or was pruned", blockNum)
	}
	if rpcBlock.Hash == nil {
		return nil, fmt.Errorf("block %d was returned without a hash", blockNum)
	}

	logs, err := blockfetcher.FetchLogs(ctx, eth.Bytes(rpcBlock.Hash.Bytes()), client)
	if err != nil {
		return nil, fmt.Errorf("fetching logs for block %d %q: %w", blockNum, rpcBlock.Hash.Pretty(), err)
	}

	ethBlock, _ := block.RpcToEthBlock(rpcBlock, nil, logs, f.logger)
	return ethBlock, nil
}

// throttle spaces block fetches fetchInterval apart, concurrent ones included:
// each claims the next free slot and sleeps until it. It runs before the block
// fetch gets its deadline, so the sleep never eats into it.
func (f *EVMFetcher) throttle() {
	if f.fetchInterval <= 0 {
		return
	}

	f.lastFetchLock.Lock()
	slot := time.Now()
	if next := f.lastFetchAt.Add(f.fetchInterval); next.After(slot) {
		slot = next
	}
	f.lastFetchAt = slot
	f.lastFetchLock.Unlock()

	time.Sleep(time.Until(slot))
}

// fetchTronBlock fetches the Tron-native block for requestBlockNum through the
// Tron client pool, with its own fallback rotation.
//
// The EVM getBlock/getLogs calls in Fetch already consumed part of this
// request's max-block-fetch-duration budget. Detaching the parent deadline with
// context.WithoutCancel gives the Tron client pool its own full per-attempt
// budget, so a slow EVM round does not make healthy Tron providers look like
// failures. Cancellation on shutdown still propagates through the poller's
// termination path rather than through this context.
func (f *EVMFetcher) fetchTronBlock(ctx context.Context, requestBlockNum uint64) (*pbtron.Block, error) {
	tronCtx := context.WithoutCancel(ctx)
	return rpc.WithClientsContext(f.tronClients, tronCtx,
		func(ctx context.Context, client pbtronapi.WalletClient) (*pbtron.Block, error) {
			out, err := f.tronFetcher.fetch(ctx, client, requestBlockNum)
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	)
}

func ethBlockLIBNum(b *pbeth.Block) uint64 {
	if b.Number <= bstream.GetProtocolFirstStreamableBlock+200 {
		return bstream.GetProtocolFirstStreamableBlock
	}

	return b.Number - 200
}
