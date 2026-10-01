package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	pbtroncore "github.com/streamingfast/tron-protocol/pb/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

var errStopAfterGetBlock = errors.New("stop after get block")

// slowHeadClient reports a head of 99 until headAt (fake clock), then 100. Each
// head request fails like a real one would when its context is already done.
// GetBlockByNum2 records the deadline it is called with, then stops the fetch.
type slowHeadClient struct {
	pbtronapi.WalletClient
	headAt time.Time

	headCalls        int
	getBlockCalled   bool
	getBlockDeadline time.Time
}

func (c *slowHeadClient) GetNowBlock2(ctx context.Context, _ *pbtronapi.EmptyMessage, _ ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	c.headCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	num := int64(99)
	if !time.Now().Before(c.headAt) {
		num = 100
	}
	return &pbtronapi.BlockExtention{
		BlockHeader: &pbtroncore.BlockHeader{RawData: &pbtroncore.BlockHeaderRaw{Number: num}},
	}, nil
}

func (c *slowHeadClient) GetBlockByNum2(ctx context.Context, _ *pbtronapi.NumberMessage, _ ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	c.getBlockCalled = true
	c.getBlockDeadline, _ = ctx.Deadline()
	return nil, errStopAfterGetBlock
}

func TestFetchWaitsForHeadPastTheCallDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const budget = 3 * time.Second
		client := &slowHeadClient{headAt: time.Now().Add(5 * time.Second)}
		f := NewFetcher(0, time.Second, zlogTest)

		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()

		_, err := f.fetch(ctx, client, 100)
		require.ErrorIs(t, err, errStopAfterGetBlock, "the head wait must not fail on the call deadline")

		require.True(t, client.getBlockCalled)
		assert.Equal(t, budget, time.Until(client.getBlockDeadline),
			"the block fetch gets the full budget once the head is reached")
	})
}

func TestFetchGivesUpOnAStuckHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &slowHeadClient{headAt: time.Now().Add(time.Hour)}
		f := NewFetcher(0, time.Second, zlogTest)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		start := time.Now()
		_, err := f.fetch(ctx, client, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "head still at 99")
		assert.False(t, client.getBlockCalled)
		assert.Equal(t, maxHeadWait, time.Since(start).Round(time.Second))
	})
}

// hangingHeadClient never answers a head request before its context ends.
type hangingHeadClient struct {
	pbtronapi.WalletClient
}

func (hangingHeadClient) GetNowBlock2(ctx context.Context, _ *pbtronapi.EmptyMessage, _ ...grpc.CallOption) (*pbtronapi.BlockExtention, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestHeadWaitFailsFastOnAHungRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := NewFetcher(0, time.Second, zlogTest)

		start := time.Now()
		_, err := f.fetchLatestBlockNumUntil(context.Background(), hangingHeadClient{}, 100, 3*time.Second)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotContains(t, err.Error(), "head still at", "a hung provider is a request failure, not a lagging head")
		assert.Equal(t, 3*time.Second, time.Since(start), "the pool must be able to roll after one request timeout")
	})
}

func TestFetchWithoutDeadlineWaitsForHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &slowHeadClient{headAt: time.Now().Add(5 * time.Second)}
		f := NewFetcher(0, time.Second, zlogTest)

		_, err := f.fetch(context.Background(), client, 100)
		require.ErrorIs(t, err, errStopAfterGetBlock)
		assert.True(t, client.getBlockDeadline.IsZero(), "no deadline in, no deadline added")
		assert.Equal(t, 6, client.headCalls, "one request at t=0, then one per second until t=5s")
	})
}

func TestHeadWaitersShareTheHighestHead(t *testing.T) {
	var w headWaiter
	var wg sync.WaitGroup
	for i := int64(1); i <= 50; i++ {
		wg.Add(1)
		go func(head int64) {
			defer wg.Done()
			w.observe(head)
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int64(50), w.Latest())
}
