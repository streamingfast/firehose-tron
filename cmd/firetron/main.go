package main

import (
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

// Injected at build time
var version = ""

var logger, _ = logging.PackageLogger("firetron", "github.com/streamingfast/firehose-tron")

func main() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.InfoLevel))

	Run(
		"firetron",
		"Firehose Tron block fetching and tooling",

		ConfigureVersion(version),
		ConfigureViper("FIRETRON"),

		FetchCommand,
		FetchEVMCommand,
		TestBlockCommand,

		OnCommandErrorLogAndExit(logger),
	)
}
