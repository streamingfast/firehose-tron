package rpc

import (
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

var zlogTest, _ = logging.PackageLogger("rpc_test", "github.com/streamingfast/firehose-tron/rpc/test")

func init() {
	logging.InstantiateLoggers()
}

var _ = zap.NewNop // referenced to avoid unused import if zlogTest is temporarily unused
