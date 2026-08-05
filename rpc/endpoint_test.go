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
			rawURL:     "https://grpc.example.com?apiKey=URLKEY",
			defaultKey: "FLAGKEY",
			expectKey:  "URLKEY",
			expectDial: "grpc.example.com:443",
		},
		{
			name:       "default used when url has no key",
			rawURL:     "https://grpc.example.com",
			defaultKey: "FLAGKEY",
			expectKey:  "FLAGKEY",
			expectDial: "grpc.example.com:443",
		},
		{
			name:       "empty apiKey value treated as absent",
			rawURL:     "https://grpc.example.com?apiKey=",
			defaultKey: "FLAGKEY",
			expectKey:  "FLAGKEY",
			expectDial: "grpc.example.com:443",
		},
		{
			name:       "no scheme defaults https",
			rawURL:     "grpc.example.com",
			expectDial: "grpc.example.com:443",
			expectHTTP: false,
		},
		{
			name:       "explicit http scheme is plaintext",
			rawURL:     "http://grpc.example.com",
			expectDial: "grpc.example.com:80",
			expectHTTP: true,
		},
		{
			name:       "uppercase HTTP scheme is not double-prefixed and is plaintext",
			rawURL:     "HTTP://grpc.example.com",
			expectDial: "grpc.example.com:80",
			expectHTTP: true,
		},
		{
			name:       "uppercase HTTPS scheme is not double-prefixed",
			rawURL:     "HTTPS://grpc.example.com?apiKey=K",
			expectKey:  "K",
			expectDial: "grpc.example.com:443",
		},
		{
			name:       "apiKey param matched case-insensitively",
			rawURL:     "https://host.example.com?APIKEY=K",
			expectKey:  "K",
			expectDial: "host.example.com:443",
		},
		{
			name:           "insecure param matched case-insensitively",
			rawURL:         "https://host.example.com?Insecure=true",
			expectDial:     "host.example.com:443",
			expectInsecure: true,
		},
		{
			name:      "empty url errors",
			rawURL:    "",
			expectErr: "empty",
		},
		{
			name:      "whitespace-only url errors",
			rawURL:    "   ",
			expectErr: "empty",
		},
		{
			name:      "env var expanding to empty url errors",
			rawURL:    "${EMPTY_URL}",
			env:       map[string]string{"EMPTY_URL": ""},
			expectErr: "empty",
		},
		{
			name:       "explicit port preserved",
			rawURL:     "https://grpc.example.com:8443?apiKey=K",
			expectKey:  "K",
			expectDial: "grpc.example.com:8443",
		},
		{
			name:           "insecure true is extracted and stripped",
			rawURL:         "https://host.example.com?insecure=true&apiKey=K",
			expectKey:      "K",
			expectDial:     "host.example.com:443",
			expectInsecure: true,
		},
		{
			name:           "insecure absent defaults false",
			rawURL:         "https://host.example.com?apiKey=K",
			expectKey:      "K",
			expectDial:     "host.example.com:443",
			expectInsecure: false,
		},
		{
			name:      "insecure non-boolean errors",
			rawURL:    "https://host.example.com?insecure=yes-please",
			expectErr: "insecure",
		},
		{
			name:           "control params stripped, unrelated survives",
			rawURL:         "https://host.example.com?region=us&apiKey=K&insecure=true&tier=pro",
			expectKey:      "K",
			expectDial:     "host.example.com:443",
			expectInsecure: true,
			expectQuery:    "region=us&tier=pro",
		},
		{
			name:       "path containing scheme is not misparsed",
			rawURL:     "https://host.example.com/proxy/https://inner?apiKey=K",
			expectKey:  "K",
			expectDial: "host.example.com:443",
		},
		{
			name:       "env var in host and key positions",
			rawURL:     "${RPC_URL}?apiKey=${RPC_KEY}",
			env:        map[string]string{"RPC_URL": "https://env.example.com", "RPC_KEY": "ENVKEY"},
			expectKey:  "ENVKEY",
			expectDial: "env.example.com:443",
		},
		{
			name:      "undefined env var errors and names the variable",
			rawURL:    "https://host.example.com?apiKey=${MISSING_KEY}",
			expectErr: "MISSING_KEY",
		},
		{
			name:       "literal dollar in default key is preserved",
			rawURL:     "https://host.example.com",
			defaultKey: "abc$def",
			expectKey:  "abc$def",
			expectDial: "host.example.com:443",
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
	ep, err := ParseEndpoint("https://host.example.com?apiKey=SECRET", "")
	require.NoError(t, err)

	s := ep.String()
	assert.NotContains(t, s, "SECRET")
	assert.Contains(t, s, "apiKey=<redacted>")
	assert.Contains(t, s, "host.example.com")
}

func TestEndpointStringNoKey(t *testing.T) {
	ep, err := ParseEndpoint("https://host.example.com", "")
	require.NoError(t, err)

	s := ep.String()
	assert.NotContains(t, s, "apiKey")
	assert.Contains(t, s, "host.example.com")
}

func TestParseEndpointErrorDoesNotLeakKey(t *testing.T) {
	_, err := ParseEndpoint("https://host.example.com:notaport?apiKey=SUPERSECRET", "")
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
			raw:    "https://host.example.com?apiKey=SECRET&other=x",
			expect: "https://host.example.com?apiKey=<redacted>&other=x",
		},
		{
			name:   "key present as sole param",
			raw:    "https://host.example.com?apiKey=SECRET",
			expect: "https://host.example.com?apiKey=<redacted>",
		},
		{
			name:   "no key unchanged",
			raw:    "https://host.example.com?other=x",
			expect: "https://host.example.com?other=x",
		},
		{
			name:   "env var literal redacted",
			raw:    "https://host.example.com?apiKey=${RPC_KEY}",
			expect: "https://host.example.com?apiKey=<redacted>",
		},
		{
			name:   "uppercase key name redacted",
			raw:    "https://host.example.com?APIKEY=SECRET&other=x",
			expect: "https://host.example.com?APIKEY=<redacted>&other=x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, RedactRawURL(tt.raw))
		})
	}
}
