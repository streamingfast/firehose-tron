package rpc

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	pbtroncore "github.com/streamingfast/tron-protocol/pb/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// headAdvancingClient reports a head below the requested block for the first
// (callsUntilHead) GetNowBlock2 calls, then reports it at head. Other methods
// are inherited from the embedded WalletClient and are not expected to be
// called by fetchLatestBlockNumUntil.
type headAdvancingClient struct {
	pbtronapi.WalletClient
	callsUntilHead int
	calls          int
}

func (c *headAdvancingClient) GetNowBlock2(ctx context.Context, in *pbtronapi.EmptyMessage, opts ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	c.calls++
	num := int64(1) // below requested until enough calls have passed
	if c.calls >= c.callsUntilHead {
		num = 100
	}
	return &pbtronapi.BlockExtention{
		BlockHeader: &pbtroncore.BlockHeader{RawData: &pbtroncore.BlockHeaderRaw{Number: num}},
	}, nil
}

func TestHeadWaitAdvancesOnFakeClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := NewFetcher(0, 500*time.Millisecond, zlogTest)
		client := &headAdvancingClient{callsUntilHead: 3}

		start := time.Now()
		_, err := f.fetchLatestBlockNumUntil(context.Background(), client, 100)
		require.NoError(t, err)

		// Two 500ms sleeps elapsed on the fake clock, instantly in wall time.
		assert.GreaterOrEqual(t, time.Since(start), time.Second)
		assert.Equal(t, 3, client.calls)
	})
}
