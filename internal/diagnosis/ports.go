package diagnosis

import (
	"context"
	"fmt"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/target"
)

// PortsChecker verifies expected ports are listening.
type PortsChecker struct{}

func (c *PortsChecker) Name() string { return "port_listening" }

func (c *PortsChecker) Run(ctx context.Context, tgt target.Target, opts CheckOpts) CheckResult {
	ports := []int{opts.HTTPPort, opts.GRPCPort}
	if opts.HTTPPort == 0 {
		ports = []int{8090, 50051}
	}

	out, _ := tgt.Exec(ctx, "ss", "-tlnp")
	listening := string(out)

	var missing []int
	for _, port := range ports {
		if port == 0 {
			continue
		}
		portStr := fmt.Sprintf(":%d ", port)
		if !strings.Contains(listening, portStr) {
			missing = append(missing, port)
		}
	}

	if len(missing) > 0 {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusFail,
			Message: fmt.Sprintf("Ports not listening: %v", missing),
			Suggestions: []string{
				fmt.Sprintf("Check node status: trond status %s", opts.NodeName),
				"Review node logs: trond logs " + opts.NodeName,
				"Verify firewall rules",
			},
		}
	}

	return CheckResult{
		Name:    c.Name(),
		Status:  StatusPass,
		Message: fmt.Sprintf("All expected ports listening (%v)", ports),
	}
}
