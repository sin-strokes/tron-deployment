package main

import (
	"context"
	"os"

	"github.com/tronprotocol/tron-deployment/cmd"
	"github.com/tronprotocol/tron-deployment/internal/observability"
)

func main() {
	// Best-effort tracing setup. No-op when TROND_OTLP_ENDPOINT is
	// unset (the default), so the cold-start cost is the env-var
	// lookup plus a noop tracer-provider install.
	shutdown := observability.Init(context.Background(), cmd.Version())
	exit := cmd.Execute()
	_ = shutdown(context.Background())
	os.Exit(exit)
}
