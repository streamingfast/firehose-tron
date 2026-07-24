package rpc

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// deadlineRecordingClient embeds the WalletClient interface so only the method
// exercised by the head-wait path needs an implementation. It records the
// deadline of the context it is called with, then errors immediately.
type deadlineRecordingClient struct {
	pbtronapi.WalletClient
	mu       sync.Mutex
	called   bool
	deadline time.Time
	hasDL    bool
}

func (c *deadlineRecordingClient) GetNowBlock2(ctx context.Context, _ *pbtronapi.EmptyMessage, _ ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	c.mu.Lock()
	c.called = true
	c.deadline, c.hasDL = ctx.Deadline()
	c.mu.Unlock()
	return nil, status.Error(codes.Unavailable, "stop here")
}

func TestFetchTronBlockIgnoresParentDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &deadlineRecordingClient{}
		strategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
		clients := firecoreRPC.NewClients(2*time.Second, strategy, zlogTest)
		clients.Add(fake)

		tronFetcher := NewFetcher(0, 0, zlogTest)
		evm := NewEVMFetcher(clients, tronFetcher, 0, 0, zlogTest)

		// Parent context is already past its deadline — the exact squeeze the
		// nested EVM->Tron call hits under load.
		parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		_, err := evm.fetchTronBlock(parent, 100)
		require.Error(t, err) // fake returns Unavailable; we assert on the deadline it saw

		fake.mu.Lock()
		defer fake.mu.Unlock()
		require.True(t, fake.called, "tron client must be attempted despite the expired parent")
		require.True(t, fake.hasDL, "the pool applies its own deadline")
		assert.True(t, fake.deadline.After(time.Now()),
			"tron call must run under a live deadline, not the expired parent one")
	})
}
