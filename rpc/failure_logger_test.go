package rpc

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestFailureLoggerCollapsesRepeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.DebugLevel)
		failures := newFailureLogger(zap.New(core), 30*time.Second)

		err := errors.New("connection refused")
		for range 100 {
			failures.log("failed to fetch block", err)
			time.Sleep(100 * time.Millisecond)
		}

		warns := logs.FilterLevelExact(zap.WarnLevel).All()
		require.Len(t, warns, 1, "an identical failure must not be re-logged within the interval")
		assert.Len(t, logs.FilterLevelExact(zap.DebugLevel).All(), 99,
			"the collapsed occurrences stay available at DEBUG")
		assert.NotContains(t, warns[0].ContextMap(), "suppressed_occurrences", "nothing was suppressed yet on the first line")
	})
}

func TestFailureLoggerRelogsAfterInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		failures := newFailureLogger(zap.New(core), 30*time.Second)

		err := errors.New("connection refused")
		for range 10 {
			failures.log("failed to fetch block", err)
			time.Sleep(5 * time.Second)
		}

		warns := logs.All()
		require.Len(t, warns, 2)
		// Logged at 0s, collapsed at 5s through 25s, logged again at 30s.
		assert.Equal(t, int64(5), warns[1].ContextMap()["suppressed_occurrences"],
			"the repeat line accounts for what it stood in for")
	})
}

func TestFailureLoggerReportsANewFailureImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		failures := newFailureLogger(zap.New(core), 30*time.Second)

		failures.log("failed to fetch block", errors.New("connection refused"))
		failures.log("failed to fetch block", errors.New("connection refused"))
		failures.log("failed to fetch block", errors.New("401 unauthorized"))

		require.Len(t, logs.All(), 2, "a failure that changes is never withheld")
	})
}

func TestFailureLoggerResetOnSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		failures := newFailureLogger(zap.New(core), 30*time.Second)

		err := errors.New("connection refused")
		failures.log("failed to fetch block", err)
		failures.log("failed to fetch block", err)

		// A fallback endpoint took over, then the failure comes back: an
		// operator must see it even though the interval has not elapsed.
		failures.reset()
		failures.log("failed to fetch block", err)

		require.Len(t, logs.All(), 2)
	})
}
