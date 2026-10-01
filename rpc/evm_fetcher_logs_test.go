package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBlockHash = "0x0000000000000064b1a2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718"

// evmNode answers the three JSON-RPC calls the EVM side of fetch-evm makes and
// records the eth_getLogs params it received. Block 100 is the head unless
// headAt is set, in which case the head is 99 until then. nullBlock answers
// null to eth_getBlockByNumber, like a lagging load-balanced backend does.
type evmNode struct {
	mu            sync.Mutex
	getLogsParams map[string]any
	headCalls     int
	blockCallsAt  []time.Time
	headAt        time.Time
	nullBlock     bool
}

func (n *evmNode) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	var call struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params []json.RawMessage
	}
	if err := json.Unmarshal(body, &call); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	var result string
	switch {
	case call.Method == "eth_blockNumber":
		n.mu.Lock()
		n.headCalls++
		n.mu.Unlock()

		result = `"0x64"`
		if time.Now().Before(n.headAt) {
			result = `"0x63"`
		}
	case call.Method == "eth_getBlockByNumber" && n.nullBlock:
		result = `null`
	case call.Method == "eth_getBlockByNumber":
		n.mu.Lock()
		n.blockCallsAt = append(n.blockCallsAt, time.Now())
		n.mu.Unlock()
		result = `{
			"number": "0x64",
			"hash": "` + testBlockHash + `",
			"parentHash": "0x0000000000000063b1a2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718",
			"timestamp": "0x6553f100",
			"gasLimit": "0x0",
			"gasUsed": "0x0",
			"miner": "0x0000000000000000000000000000000000000000",
			"transactions": []
		}`
	case call.Method == "eth_getLogs":
		var params map[string]any
		if len(call.Params) != 1 || json.Unmarshal(call.Params[0], &params) != nil {
			http.Error(rw, "eth_getLogs expects one object param", http.StatusBadRequest)
			return
		}
		n.mu.Lock()
		n.getLogsParams = params
		n.mu.Unlock()
		result = `[]`
	default:
		http.Error(rw, "unexpected method "+call.Method, http.StatusBadRequest)
		return
	}

	rw.Write([]byte(`{"jsonrpc":"2.0","id":` + string(call.ID) + `,"result":` + result + `}`))
}

// fetch-evm skips receipts, so logs come from an eth_getLogs query that
// carries only the block hash, with no fromBlock/toBlock bounds.
func TestEVMFetcherFetchesLogsByBlockHash(t *testing.T) {
	node := &evmNode{}
	server := httptest.NewServer(node)
	defer server.Close()

	clients := firecoreRPC.NewClients(0, firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient](), zlogTest)
	evm := NewEVMFetcher(clients, NewFetcher(0, 0, zlogTest), 0, 0, zlogTest)

	block, err := evm.fetchEVMBlock(context.Background(), ethRPC.NewClient(server.URL), 100)
	require.NoError(t, err)
	assert.Equal(t, uint64(100), block.Number)

	node.mu.Lock()
	defer node.mu.Unlock()
	require.NotNil(t, node.getLogsParams, "eth_getLogs must be called")
	assert.Equal(t, testBlockHash, node.getLogsParams["blockHash"])
	assert.NotContains(t, node.getLogsParams, "fromBlock")
	assert.NotContains(t, node.getLogsParams, "toBlock")
}

// A load-balanced endpoint can answer null for a block its eth_blockNumber
// already covers. That must come back as an error the poller retries.
func TestEVMFetcherNullBlockIsAnError(t *testing.T) {
	server := httptest.NewServer(&evmNode{nullBlock: true})
	defer server.Close()

	clients := firecoreRPC.NewClients(0, firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient](), zlogTest)
	evm := NewEVMFetcher(clients, NewFetcher(0, 0, zlogTest), 0, 0, zlogTest)

	_, _, err := evm.Fetch(context.Background(), ethRPC.NewClient(server.URL), 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// A head already seen to cover the next block is not asked for again, and
// --interval-between-fetch spaces the block fetches themselves.
func TestEVMFetcherReusesTheHeadAndThrottles(t *testing.T) {
	const interval = 200 * time.Millisecond
	node := &evmNode{}
	server := httptest.NewServer(node)
	defer server.Close()

	tronClients := firecoreRPC.NewClients(time.Second, firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient](), zlogTest)
	tronClients.Add(&deadlineRecordingClient{})
	evm := NewEVMFetcher(tronClients, NewFetcher(0, 0, zlogTest), interval, 0, zlogTest)
	client := ethRPC.NewClient(server.URL)

	for _, blockNum := range []uint64{99, 100} {
		_, _, err := evm.Fetch(context.Background(), client, blockNum)
		require.ErrorContains(t, err, "stop here", "the EVM side must succeed before the Tron side is reached")
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	assert.Equal(t, 1, node.headCalls, "head 100 seen for block 99 already covers block 100")
	require.Len(t, node.blockCallsAt, 2)
	assert.GreaterOrEqual(t, node.blockCallsAt[1].Sub(node.blockCallsAt[0]), interval)
}

// The EVM side waits for its head outside the pool's per-call deadline, like
// the Tron side, then fetches the block under a fresh one.
func TestEVMFetcherWaitsForHeadPastTheCallDeadline(t *testing.T) {
	const budget = 300 * time.Millisecond
	node := &evmNode{headAt: time.Now().Add(3 * budget)}
	server := httptest.NewServer(node)
	defer server.Close()

	tronClients := firecoreRPC.NewClients(time.Second, firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient](), zlogTest)
	tronClients.Add(&deadlineRecordingClient{})
	evm := NewEVMFetcher(tronClients, NewFetcher(0, 0, zlogTest), 0, 50*time.Millisecond, zlogTest)

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	_, _, err := evm.Fetch(ctx, ethRPC.NewClient(server.URL), 100)
	require.Error(t, err, "the fake Tron client always fails")
	assert.Contains(t, err.Error(), "stop here", "the EVM side must succeed before the Tron side is reached")
	assert.Equal(t, int64(100), evm.evmHead.Latest())
}
