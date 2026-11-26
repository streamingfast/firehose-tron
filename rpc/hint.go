package rpc

import (
	"errors"
	"strings"

	ethRPC "github.com/streamingfast/eth-go/rpc"
	"go.uber.org/zap"
)

// errorCodePrunedHistory is the JSON-RPC error code java-tron 4.8.3+ answers
// when the requested block, receipts or logs are no longer, or were never,
// stored by the node: a Lite FullNode below its retained range, or a node
// started with `storage.transHistory.switch = "off"`.
const errorCodePrunedHistory ethRPC.ErrorCode = 4444

// FailureHint turns the most common endpoint misconfigurations into the fix,
// since the raw transport error rarely reads as one. It returns an empty string
// when nothing recognizable is found.
func FailureHint(err error) string {
	if err == nil {
		return ""
	}

	if rpcErr, ok := errors.AsType[*ethRPC.ErrResponse](err); ok && rpcErr.Code == errorCodePrunedHistory {
		return "the endpoint does not have this block's history: it is a Lite FullNode or runs with storage.transHistory.switch = \"off\", point it at a FullNode keeping full block and transaction history"
	}

	message := err.Error()

	switch {
	case strings.Contains(message, "first record does not look like a TLS handshake"):
		return "the endpoint answered in plaintext: an endpoint without an explicit scheme is dialed over TLS, prefix it with http:// to dial it in plaintext (TronGrid's grpc.trongrid.io:50051 is plaintext)"

	case strings.Contains(message, "x509:") || strings.Contains(message, "certificate"):
		return "TLS certificate validation failed: add ?insecure=true to the endpoint to skip validation, or use http:// if the endpoint is plaintext"

	case strings.Contains(message, "401") || strings.Contains(message, "403") ||
		strings.Contains(message, "Unauthenticated") || strings.Contains(message, "PermissionDenied"):
		return "the endpoint rejected our credentials: check the API key carried by ?apiKey=..."
	}

	return ""
}

// hintField renders FailureHint as a zap field, or as a no-op field when there
// is no hint to give, so it can be passed unconditionally.
func hintField(err error) zap.Field {
	if hint := FailureHint(err); hint != "" {
		return zap.String("hint", hint)
	}

	return zap.Skip()
}
