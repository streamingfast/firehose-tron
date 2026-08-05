package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// failingWalletClient embeds the WalletClient interface so only the first call
// of the fetch path needs an implementation, and fails it the way an
// unauthenticated provider would.
type failingWalletClient struct {
	pbtronapi.WalletClient
}

func (c *failingWalletClient) GetNowBlock2(context.Context, *pbtronapi.EmptyMessage, ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	return nil, status.Error(codes.Unauthenticated, "invalid api key")
}

// tlsHandshakeFailingClient reproduces what a plaintext gRPC endpoint dialed
// over TLS answers.
type tlsHandshakeFailingClient struct {
	pbtronapi.WalletClient
}

func (c *tlsHandshakeFailingClient) GetNowBlock2(context.Context, *pbtronapi.EmptyMessage, ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	return nil, status.Error(codes.Unavailable, `connection error: desc = "transport: authentication handshake failed: tls: first record does not look like a TLS handshake"`)
}

// The block poller retries failed fetches forever without logging anything, so
// a permanently failing endpoint (bad API key, rate limit) looks like a poller
// frozen on one block. Both fetchers must log the underlying error themselves.
func TestFetcherLogsFetchFailure(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)

	fetcher := NewFetcher(0, 0, zap.New(core))
	_, _, err := fetcher.Fetch(context.Background(), &failingWalletClient{}, 42)
	require.Error(t, err)

	entries := logs.FilterMessageSnippet("failed to fetch block").All()
	require.Len(t, entries, 1)
	assert.Equal(t, uint64(42), entries[0].ContextMap()["block_num"])
	assert.Contains(t, entries[0].ContextMap()["error"], "invalid api key")
	assert.Contains(t, entries[0].ContextMap()["hint"], "apiKey", "a rejected key must come with the fix")
}

// The bare `host:port` form is dialed over TLS, so pointing it at a plaintext
// gRPC port fails every single fetch. The transport error does not say what to
// change, so the log line has to.
func TestFetcherLogsPlaintextHint(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)

	fetcher := NewFetcher(0, 0, zap.New(core))
	_, _, err := fetcher.Fetch(context.Background(), &tlsHandshakeFailingClient{}, 42)
	require.Error(t, err)

	entries := logs.FilterMessageSnippet("failed to fetch block").All()
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].ContextMap()["hint"], "http://")
}

func TestFailureHint(t *testing.T) {
	assert.Empty(t, FailureHint(nil))
	assert.Empty(t, FailureHint(errors.New("context deadline exceeded")))
	assert.Contains(t, FailureHint(errors.New(`tls: first record does not look like a TLS handshake`)), "http://")
	assert.Contains(t, FailureHint(errors.New(`x509: certificate signed by unknown authority`)), "insecure=true")
}

func TestEVMFetcherLogsFetchFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	strategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
	tronClients := firecoreRPC.NewClients(time.Second, strategy, logger)
	tronClients.Add(&failingWalletClient{})

	evm := NewEVMFetcher(tronClients, NewFetcher(0, 0, logger), 0, 0, logger)

	_, _, err := evm.Fetch(context.Background(), ethRPC.NewClient(server.URL), 42)
	require.Error(t, err)

	entries := logs.FilterMessageSnippet("failed to fetch block").All()
	require.Len(t, entries, 1)
	assert.Equal(t, uint64(42), entries[0].ContextMap()["block_num"])
}
