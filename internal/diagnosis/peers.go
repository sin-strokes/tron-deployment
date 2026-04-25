package diagnosis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tronprotocol/tron-deployment/internal/target"
)

// PeersChecker verifies the node has sufficient peer connections.
type PeersChecker struct{}

func (c *PeersChecker) Name() string { return "peer_count" }

func (c *PeersChecker) Run(ctx context.Context, tgt target.Target, opts CheckOpts) CheckResult {
	if opts.HTTPPort == 0 {
		opts.HTTPPort = 8090
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/wallet/listnodes", opts.HTTPPort)
	out, err := tgt.Exec(ctx, "curl", "-s", "--max-time", "5", url)
	if err != nil {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Cannot reach node API to check peers",
		}
	}

	var resp struct {
		Nodes []json.RawMessage `json:"nodes"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not parse peer list response",
		}
	}

	count := len(resp.Nodes)
	minPeers := 3
	if opts.Network == "private" {
		minPeers = 1
	}

	if count < minPeers {
		return CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("Only %d peers connected (minimum %d recommended)", count, minPeers),
			Suggestions: []string{
				"Check network connectivity",
				"Verify seed nodes in config",
				"Wait for peer discovery (can take several minutes)",
			},
		}
	}

	return CheckResult{
		Name:    c.Name(),
		Status:  StatusPass,
		Message: fmt.Sprintf("%d peers connected", count),
	}
}
