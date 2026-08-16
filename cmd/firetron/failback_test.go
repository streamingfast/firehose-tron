package main

import (
	"context"
	"errors"
	"testing"
	"time"

	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestFailbackEnabled(t *testing.T) {
	cases := []struct {
		name          string
		endpointCount int
		interval      time.Duration
		expected      bool
	}{
		{"two endpoints and an interval", 2, time.Minute, true},
		{"a single endpoint has nothing to fail back from", 1, time.Minute, false},
		{"no endpoint at all", 0, time.Minute, false},
		{"zero interval opts out", 2, 0, false},
		{"negative interval opts out", 2, -time.Minute, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expected, failbackEnabled(c.endpointCount, c.interval))
		})
	}
}

// The pools use a sticky rolling strategy: once a transient error rolls polling
// to a fallback endpoint, every later call keeps using that fallback. This is
// the regression the failback ticker exists to bound, so assert both halves,
// that it sticks, and that Reset undoes it.
func TestResetReturnsToThePreferredEndpoint(t *testing.T) {
	clients := firecoreRPC.NewClients(
		time.Second,
		firecoreRPC.NewStickyRollingStrategy[string](),
		zap.NewNop(),
	)
	clients.Add("preferred")
	clients.Add("fallback")

	call := func(failOn string) (string, error) {
		return firecoreRPC.WithClients(clients, func(_ context.Context, client string) (string, error) {
			if client == failOn {
				return "", errors.New("transient failure")
			}
			return client, nil
		})
	}

	used, err := call("")
	require.NoError(t, err)
	require.Equal(t, "preferred", used, "the declared order is used first")

	used, err = call("preferred")
	require.NoError(t, err)
	require.Equal(t, "fallback", used, "a failing endpoint rolls to the next one")

	used, err = call("")
	require.NoError(t, err)
	assert.Equal(t, "fallback", used,
		"without a reset the pool stays on the fallback even though the preferred endpoint recovered")

	clients.Reset()

	used, err = call("")
	require.NoError(t, err)
	assert.Equal(t, "preferred", used, "reset brings polling back to the preferred endpoint")
}

func TestStartEndpointFailbackResetsOnItsInterval(t *testing.T) {
	clients := firecoreRPC.NewClients(
		time.Second,
		firecoreRPC.NewStickyRollingStrategy[string](),
		zap.NewNop(),
	)
	clients.Add("preferred")
	clients.Add("fallback")

	call := func(failOn string) (string, error) {
		return firecoreRPC.WithClients(clients, func(_ context.Context, client string) (string, error) {
			if client == failOn {
				return "", errors.New("transient failure")
			}
			return client, nil
		})
	}

	_, err := call("preferred")
	require.NoError(t, err)

	startEndpointFailback(zap.NewNop(), "Tron", clients, 2, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		used, err := call("")
		return err == nil && used == "preferred"
	}, 2*time.Second, 10*time.Millisecond, "the ticker should re-prefer the declared order")
}

// A pool that cannot fail back must not spawn a ticker that resets it anyway.
func TestStartEndpointFailbackIsANoOpWhenDisabled(t *testing.T) {
	clients := firecoreRPC.NewClients(
		time.Second,
		firecoreRPC.NewStickyRollingStrategy[string](),
		zap.NewNop(),
	)
	clients.Add("preferred")
	clients.Add("fallback")

	call := func(failOn string) (string, error) {
		return firecoreRPC.WithClients(clients, func(_ context.Context, client string) (string, error) {
			if client == failOn {
				return "", errors.New("transient failure")
			}
			return client, nil
		})
	}

	_, err := call("preferred")
	require.NoError(t, err)

	startEndpointFailback(zap.NewNop(), "Tron", clients, 2, 0)

	time.Sleep(100 * time.Millisecond)

	used, err := call("")
	require.NoError(t, err)
	assert.Equal(t, "fallback", used, "a zero interval must leave the pool on the fallback")
}
