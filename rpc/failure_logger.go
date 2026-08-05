package rpc

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// failureLogInterval is how often a failure that keeps repeating identically is
// re-logged at WARN. A poller retries a few times per second, and a fallback
// pool can have one permanently broken endpoint while still producing blocks
// through another, so logging every occurrence would drown the log.
const failureLogInterval = 30 * time.Second

// failureLogger logs endpoint failures at WARN, but collapses an identical
// failure that keeps repeating into one line per failureLogInterval. The
// occurrences in between are still logged at DEBUG and counted, so nothing is
// lost, and a first failure or a change of failure is always reported
// immediately.
//
// reset must be called on success so that a failure coming back after a healthy
// period is reported at once rather than being taken for the tail of the
// previous burst.
type failureLogger struct {
	logger   *zap.Logger
	interval time.Duration

	mutex      sync.Mutex
	signature  string
	lastLogAt  time.Time
	suppressed int
}

func newFailureLogger(logger *zap.Logger, interval time.Duration) *failureLogger {
	return &failureLogger{logger: logger, interval: interval}
}

func (f *failureLogger) log(message string, err error, fields ...zap.Field) {
	f.mutex.Lock()

	signature := err.Error()
	repeated := signature == f.signature
	suppressed := f.suppressed

	if repeated && time.Since(f.lastLogAt) < f.interval {
		f.suppressed++
		f.mutex.Unlock()

		f.logger.Debug(message, append(fields, zap.Error(err), hintField(err))...)
		return
	}

	f.signature = signature
	f.lastLogAt = time.Now()
	f.suppressed = 0
	f.mutex.Unlock()

	fields = append(fields, zap.Error(err), hintField(err))
	if repeated {
		fields = append(fields,
			zap.Int("suppressed_occurrences", suppressed),
			zap.Duration("suppressing_for", f.interval),
		)
	}

	f.logger.Warn(message, fields...)
}

// reset forgets the current failure so the next one is logged right away.
func (f *failureLogger) reset() {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.signature = ""
	f.suppressed = 0
}
