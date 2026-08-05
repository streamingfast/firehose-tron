package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAPIKeyFlag(t *testing.T) {
	tests := []struct {
		name      string
		apiKey    string
		expectErr string
	}{
		{name: "empty is fine", apiKey: ""},
		{name: "regular key is fine", apiKey: "abcd1234-5678-90ab-cdef-1234567890ab"},
		{name: "literal dollar is fine", apiKey: "abc$def"},
		{
			name:      "unexpanded shell substitution errors",
			apiKey:    "$(TRONGRID_TRON_MAINNET_API_KEY)",
			expectErr: "TRONGRID_TRON_MAINNET_API_KEY",
		},
		{
			name:      "unexpanded env reference errors",
			apiKey:    "${TRON_API_KEY}",
			expectErr: "TRON_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAPIKeyFlag(tt.apiKey)
			if tt.expectErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectErr)
		})
	}
}
