package rpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTronClientAcceptsEndpoint(t *testing.T) {
	ep, err := ParseEndpoint("https://grpc.provider.io?apiKey=K", "")
	require.NoError(t, err)

	client, err := NewTronClient(ep)
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewTronClientPlaintextEndpoint(t *testing.T) {
	ep, err := ParseEndpoint("http://grpc.provider.io:50051", "")
	require.NoError(t, err)

	client, err := NewTronClient(ep)
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewTronClientInsecureEndpoint(t *testing.T) {
	ep, err := ParseEndpoint("https://grpc.provider.io?insecure=true", "")
	require.NoError(t, err)

	client, err := NewTronClient(ep)
	require.NoError(t, err)
	assert.NotNil(t, client)
}
