package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestProbeEndpointsAllFailing(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)

	err := probeEndpoints(context.Background(), zap.New(core), "EVM", []endpointProbe{
		{name: "https://first.example.com", probe: func(context.Context) (uint64, error) { return 0, errors.New("401 unauthorized") }},
		{name: "https://second.example.com", probe: func(context.Context) (uint64, error) { return 0, errors.New("403 rate limited") }},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "401 unauthorized")
	assert.Contains(t, err.Error(), "403 rate limited")
	assert.Contains(t, err.Error(), "API key")
	assert.Len(t, logs.FilterMessageSnippet("endpoint is not reachable").All(), 2)
}

func TestProbeEndpointsOneWorkingIsEnough(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)

	err := probeEndpoints(context.Background(), zap.New(core), "Tron", []endpointProbe{
		{name: "https://first.example.com", probe: func(context.Context) (uint64, error) { return 0, errors.New("401 unauthorized") }},
		{name: "https://second.example.com", probe: func(context.Context) (uint64, error) { return 12345, nil }},
	})

	require.NoError(t, err)
	assert.Len(t, logs.FilterMessageSnippet("endpoint is not reachable").All(), 1,
		"the failing endpoint is still reported, the poller just does not refuse to start")
}

// A bare host:port endpoint is dialed over TLS, so pointing one at a plaintext
// gRPC port fails every fetch forever. The transport error does not say that,
// so the hint has to.
func TestProbeEndpointsHintsAtPlaintextEndpoint(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)

	err := probeEndpoints(context.Background(), zap.New(core), "Tron", []endpointProbe{{
		name: "https://grpc.example.com:50051",
		probe: func(context.Context) (uint64, error) {
			return 0, errors.New(`rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: tls: first record does not look like a TLS handshake"`)
		},
	}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "http://")

	entries := logs.FilterMessageSnippet("endpoint is not reachable").All()
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].ContextMap()["hint"], "http://")
}

func TestProbeEndpointsNoProbes(t *testing.T) {
	require.NoError(t, probeEndpoints(context.Background(), zap.NewNop(), "EVM", nil))
}
