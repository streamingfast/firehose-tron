package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/streamingfast/firehose-tron/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEVMClientSendsPerEndpointKey(t *testing.T) {
	var (
		mu         sync.Mutex
		gotKey     string
		gotRawPath string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotKey = r.Header.Get("TRON-PRO-API-KEY")
		gotRawPath = r.URL.RequestURI()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer server.Close()

	ep, err := rpc.ParseEndpoint(server.URL+"?apiKey=EVMKEY", "")
	require.NoError(t, err)

	client := newEVMClient(ep)
	_, err = client.LatestBlockNum(context.Background())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "EVMKEY", gotKey)
	assert.NotContains(t, gotRawPath, "apiKey", "key must not ride in the request URL")
}
