package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBlockHash = "0x0000000000000064b1a2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718"

// evmNode answers the three JSON-RPC calls the EVM side of fetch-evm makes and
// records the eth_getLogs params it received.
type evmNode struct {
	mu            sync.Mutex
	getLogsParams map[string]any
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
	switch call.Method {
	case "eth_blockNumber":
		result = `"0x64"`
	case "eth_getBlockByNumber":
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
	case "eth_getLogs":
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

	block, err := evm.evmFetcher.FetchPBEth(context.Background(), ethRPC.NewClient(server.URL), 100)
	require.NoError(t, err)
	assert.Equal(t, uint64(100), block.Number)

	node.mu.Lock()
	defer node.mu.Unlock()
	require.NotNil(t, node.getLogsParams, "eth_getLogs must be called")
	assert.Equal(t, testBlockHash, node.getLogsParams["blockHash"])
	assert.NotContains(t, node.getLogsParams, "fromBlock")
	assert.NotContains(t, node.getLogsParams, "toBlock")
}
