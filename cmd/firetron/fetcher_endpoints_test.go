package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTronEndpoints(t *testing.T) {
	eps, err := parseTronEndpoints(
		[]string{"https://a.io?apiKey=KA&insecure=true", "https://b.io"},
		"DEFAULT",
	)
	require.NoError(t, err)
	require.Len(t, eps, 2)
	assert.Equal(t, "KA", eps[0].APIKey)
	assert.True(t, eps[0].Insecure)
	assert.Equal(t, "DEFAULT", eps[1].APIKey)
	assert.False(t, eps[1].Insecure)
}

func TestParseTronEndpointsRejectsEmpty(t *testing.T) {
	_, err := parseTronEndpoints(nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one")
}

func TestParseTronEndpointsPropagatesEnvError(t *testing.T) {
	_, err := parseTronEndpoints([]string{"https://a.io?apiKey=${DEFINITELY_MISSING_VAR}"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEFINITELY_MISSING_VAR")
}
