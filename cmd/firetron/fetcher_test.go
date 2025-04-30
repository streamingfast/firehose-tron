package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_blockTypeURL(t *testing.T) {
	require.Equal(t, "type.googleapis.com/sf.tron.type.v1.Block", blockTypeURL())
}
