package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestWarnDeprecatedAPIKeyFlag(t *testing.T) {
	t.Run("warns when key provided", func(t *testing.T) {
		core, recorded := observer.New(zap.WarnLevel)
		warnDeprecatedAPIKeyFlag(zap.New(core), "some-key")

		require.Equal(t, 1, recorded.Len())
		entry := recorded.All()[0]
		assert.Equal(t, zap.WarnLevel, entry.Level)
		assert.Contains(t, entry.Message, "deprecated")
		assert.Contains(t, entry.Message, "?apiKey=")
	})

	t.Run("silent when key empty", func(t *testing.T) {
		core, recorded := observer.New(zap.WarnLevel)
		warnDeprecatedAPIKeyFlag(zap.New(core), "")

		assert.Equal(t, 0, recorded.Len())
	})
}
