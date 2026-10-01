package rpc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// maxHeadWait bounds how long a fetch waits for the chain to produce the
// requested block. It is ten Tron slots: a provider whose head stays behind for
// that long is treated as failing, so the pool rolls to the next one.
const maxHeadWait = 30 * time.Second

// minHeadRetryInterval keeps a zero --latest-block-retry-interval from polling
// a lagging head in a tight loop for all of maxHeadWait.
const minHeadRetryInterval = 100 * time.Millisecond

// headWaiter waits for a chain head to reach a block outside the deadline the
// provider pool gives each call. That deadline is sized for fetching one block;
// waiting for the chain to produce the block routinely outlasts it at head, and
// failing there rolls the pool to the next provider for no reason.
type headWaiter struct {
	retryInterval time.Duration
	latest        atomic.Int64
}

// Latest is the highest head observed so far.
func (w *headWaiter) Latest() int64 {
	return w.latest.Load()
}

func (w *headWaiter) observe(head int64) {
	for {
		current := w.latest.Load()
		if head <= current || w.latest.CompareAndSwap(current, head) {
			return
		}
	}
}

// waitFor returns once fetchHead reports a head of at least target, polling
// every retryInterval for at most maxHeadWait. Each head request gets
// callTimeout of its own when it is positive.
//
// The wait ignores ctx's deadline and its cancellation. Every caller derives ctx
// from context.Background (firehose-core's rpc.WithClients, fetchTronBlock), so
// shutdown goes through the poller's termination, not through a fetch in flight.
func (w *headWaiter) waitFor(ctx context.Context, target int64, callTimeout time.Duration, fetchHead func(context.Context) (int64, error)) (int64, error) {
	head := w.latest.Load()
	if head >= target {
		return head, nil
	}

	waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxHeadWait)
	defer cancel()

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			select {
			case <-waitCtx.Done():
				return 0, stuckHeadError(head, target, nil)
			case <-time.After(max(w.retryInterval, minHeadRetryInterval)):
			}
		}

		callCtx, callCancel := waitCtx, context.CancelFunc(func() {})
		if callTimeout > 0 {
			callCtx, callCancel = context.WithTimeout(waitCtx, callTimeout)
		}
		observed, err := fetchHead(callCtx)
		callCancel()
		if err != nil {
			if waitCtx.Err() != nil {
				return 0, stuckHeadError(head, target, err)
			}
			return 0, fmt.Errorf("waiting for latest block num: %w", err)
		}

		head = observed
		w.observe(head)
		if head >= target {
			return head, nil
		}
	}
}

func stuckHeadError(head, target int64, lastErr error) error {
	if lastErr != nil {
		return fmt.Errorf("waiting for latest block num: head still at %d after %s, requested %d, last request: %w", head, maxHeadWait, target, lastErr)
	}
	return fmt.Errorf("waiting for latest block num: head still at %d after %s, requested %d", head, maxHeadWait, target)
}

// callBudget is the time left on ctx's deadline, or 0 when it has none.
func callBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

// withBudget gives ctx a fresh deadline of budget, detached from its current
// one, so a fetch that follows a head wait gets the full time the pool allows.
// A zero budget leaves ctx as is.
func withBudget(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if budget <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), budget)
}
