package rpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name           string
		rawURL         string
		defaultKey     string
		env            map[string]string
		expectKey      string
		expectDial     string
		expectHTTP     bool
		expectInsecure bool
		expectQuery    string // raw query on the resulting URL, control params must be gone
		expectErr      string
	}{
		{
			name:       "key in url wins over default",
			rawURL:     "https://grpc.provider.io?apiKey=URLKEY",
			defaultKey: "FLAGKEY",
			expectKey:  "URLKEY",
			expectDial: "grpc.provider.io:443",
		},
		{
			name:       "default used when url has no key",
			rawURL:     "https://grpc.provider.io",
			defaultKey: "FLAGKEY",
			expectKey:  "FLAGKEY",
			expectDial: "grpc.provider.io:443",
		},
		{
			name:       "empty apiKey value treated as absent",
			rawURL:     "https://grpc.provider.io?apiKey=",
			defaultKey: "FLAGKEY",
			expectKey:  "FLAGKEY",
			expectDial: "grpc.provider.io:443",
		},
		{
			name:       "no scheme defaults https",
			rawURL:     "grpc.provider.io",
			expectDial: "grpc.provider.io:443",
			expectHTTP: false,
		},
		{
			name:       "explicit http scheme is plaintext",
			rawURL:     "http://grpc.provider.io",
			expectDial: "grpc.provider.io:80",
			expectHTTP: true,
		},
		{
			name:       "explicit port preserved",
			rawURL:     "https://grpc.provider.io:8443?apiKey=K",
			expectKey:  "K",
			expectDial: "grpc.provider.io:8443",
		},
		{
			name:           "insecure true is extracted and stripped",
			rawURL:         "https://host.io?insecure=true&apiKey=K",
			expectKey:      "K",
			expectDial:     "host.io:443",
			expectInsecure: true,
		},
		{
			name:           "insecure absent defaults false",
			rawURL:         "https://host.io?apiKey=K",
			expectKey:      "K",
			expectDial:     "host.io:443",
			expectInsecure: false,
		},
		{
			name:      "insecure non-boolean errors",
			rawURL:    "https://host.io?insecure=yes-please",
			expectErr: "insecure",
		},
		{
			name:           "control params stripped, unrelated survives",
			rawURL:         "https://host.io?region=us&apiKey=K&insecure=true&tier=pro",
			expectKey:      "K",
			expectDial:     "host.io:443",
			expectInsecure: true,
			expectQuery:    "region=us&tier=pro",
		},
		{
			name:       "path containing scheme is not misparsed",
			rawURL:     "https://host.io/proxy/https://inner?apiKey=K",
			expectKey:  "K",
			expectDial: "host.io:443",
		},
		{
			name:       "env var in host and key positions",
			rawURL:     "${RPC_URL}?apiKey=${RPC_KEY}",
			env:        map[string]string{"RPC_URL": "https://env.provider.io", "RPC_KEY": "ENVKEY"},
			expectKey:  "ENVKEY",
			expectDial: "env.provider.io:443",
		},
		{
			name:      "undefined env var errors and names the variable",
			rawURL:    "https://host.io?apiKey=${MISSING_KEY}",
			expectErr: "MISSING_KEY",
		},
		{
			name:       "literal dollar in default key is preserved",
			rawURL:     "https://host.io",
			defaultKey: "abc$def",
			expectKey:  "abc$def",
			expectDial: "host.io:443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			ep, err := ParseEndpoint(tt.rawURL, tt.defaultKey)
			if tt.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErr)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.expectKey, ep.APIKey)
			assert.Equal(t, tt.expectDial, ep.DialTarget())
			assert.Equal(t, tt.expectHTTP, ep.Plaintext())
			assert.Equal(t, tt.expectInsecure, ep.Insecure)
			assert.NotContains(t, ep.URL.RawQuery, "apiKey")
			assert.NotContains(t, ep.URL.RawQuery, "insecure")
			if tt.expectQuery != "" {
				assert.Equal(t, tt.expectQuery, ep.URL.RawQuery)
			}
		})
	}
}

func TestEndpointStringRedacts(t *testing.T) {
	ep, err := ParseEndpoint("https://host.io?apiKey=SECRET", "")
	require.NoError(t, err)

	s := ep.String()
	assert.NotContains(t, s, "SECRET")
	assert.Contains(t, s, "apiKey=<redacted>")
	assert.Contains(t, s, "host.io")
}

func TestEndpointStringNoKey(t *testing.T) {
	ep, err := ParseEndpoint("https://host.io", "")
	require.NoError(t, err)

	s := ep.String()
	assert.NotContains(t, s, "apiKey")
	assert.Contains(t, s, "host.io")
}

func TestParseEndpointErrorDoesNotLeakKey(t *testing.T) {
	_, err := ParseEndpoint("https://host.io:notaport?apiKey=SUPERSECRET", "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SUPERSECRET")
	assert.Contains(t, err.Error(), "<redacted>")
}

func TestRedactRawURL(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		expect string
	}{
		{
			name:   "key present with trailing param",
			raw:    "https://host.io?apiKey=SECRET&other=x",
			expect: "https://host.io?apiKey=<redacted>&other=x",
		},
		{
			name:   "key present as sole param",
			raw:    "https://host.io?apiKey=SECRET",
			expect: "https://host.io?apiKey=<redacted>",
		},
		{
			name:   "no key unchanged",
			raw:    "https://host.io?other=x",
			expect: "https://host.io?other=x",
		},
		{
			name:   "env var literal redacted",
			raw:    "https://host.io?apiKey=${RPC_KEY}",
			expect: "https://host.io?apiKey=<redacted>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, RedactRawURL(tt.raw))
		})
	}
}
