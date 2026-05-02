package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/state"
	"github.com/tronprotocol/tron-deployment/internal/target"
)

// LiveStatus issues a small set of cheap HTTP probes against a
// running node and returns the discovered fields (block_height,
// is_synced, peer_count). Errors are silently dropped — the caller
// sees a key appear or not, never a failure on the whole call.
//
// The probe path differs by runtime: docker nodes get reached via
// `docker exec <name> curl ...` so the request sees the container's
// network (host port mapping may be delayed on first start). Jar
// nodes hit the host-side curl directly.
//
// Lives in internal/apply alongside WaitForReady because both are
// "operate on a deployed node via target.Target" primitives. Used
// by `trond status`, the MCP `status` tool, and any caller that
// wants the same combined state + live view.
func LiveStatus(ctx context.Context, tgt target.Target, node *state.ManagedNode) map[string]any {
	out := map[string]any{}
	if tgt == nil || node == nil {
		return out
	}
	port := node.HTTPPort
	if port == 0 {
		port = 8090
	}

	probe := func(path string) ([]byte, error) {
		url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
		if node.Runtime == "jar" {
			return tgt.Exec(ctx, "curl", "-fsS", "--max-time", "2", url)
		}
		return tgt.Exec(ctx, "docker", "exec", node.Name, "curl", "-fsS", "--max-time", "2", url)
	}

	if data, err := probe("/wallet/getnowblock"); err == nil {
		var block struct {
			BlockHeader struct {
				RawData struct {
					Number    int64 `json:"number"`
					Timestamp int64 `json:"timestamp"`
				} `json:"raw_data"`
			} `json:"block_header"`
		}
		if json.Unmarshal(data, &block) == nil {
			out["block_height"] = block.BlockHeader.RawData.Number
			if block.BlockHeader.RawData.Timestamp > 0 {
				// "synced" heuristic: tip within 60s of now. Good enough
				// for dashboards; not a consensus-level claim.
				lag := time.Since(time.UnixMilli(block.BlockHeader.RawData.Timestamp))
				out["is_synced"] = lag < 60*time.Second
			}
		}
	}

	if data, err := probe("/wallet/listnodes"); err == nil {
		var nodes struct {
			Nodes []any `json:"nodes"`
		}
		if json.Unmarshal(data, &nodes) == nil {
			out["peer_count"] = len(nodes.Nodes)
		}
	}

	return out
}
