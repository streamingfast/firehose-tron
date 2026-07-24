package rpc

import (
	"github.com/streamingfast/logging"
)

var zlogTest, _ = logging.PackageLogger("rpc_test", "github.com/streamingfast/firehose-tron/rpc/test")

func init() {
	logging.InstantiateLoggers()
}
