package apply

import (
	"context"
	"fmt"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/target"
)

// WaitForReady polls the node's HTTP API via `docker exec` until it
// responds 2xx or the timeout elapses. Used by Apply when Wait=true
// and exposed publicly so callers (verify command, MCP tool, recipe
// step) can reuse the same probe shape without duplicating the curl
// invocation.
//
// The probe runs `docker exec <name> curl ...` so it sees the
// container's network — important when host-port mapping is delayed
// on first start.
func WaitForReady(ctx context.Context, tgt target.Target, name string, httpPort int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if httpPort == 0 {
		httpPort = 8090
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/wallet/getnowblock", httpPort)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var lastErr error
	for {
		_, err := tgt.Exec(ctx, "docker", "exec", name, "curl", "-fsS", "--max-time", "5", url)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			// lastErr is always non-nil here because we only reach
			// the select after a failed probe; surface both for
			// debuggability.
			return fmt.Errorf("%s (last probe error: %v)", ctx.Err(), lastErr)
		case <-tick.C:
		}
	}
}
