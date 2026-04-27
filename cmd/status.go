package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var statusCmd = &cobra.Command{
	Use:   "status [node]",
	Short: "Show node status (or list all nodes)",
	Long: `Without arguments: list all managed nodes. With a node name: show
detailed status including (best-effort) live block height, peer count,
sync state, and the running endpoints. Network probes use the same
HTTP API endpoints inspect/diagnose use; failures are surfaced inline
rather than failing the whole command.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	if len(args) == 0 {
		return runList(cmd, args)
	}

	name := args[0]

	store, err := state.NewStore(statePath())
	if err != nil {
		return err
	}

	deployState, err := store.Load()
	if err != nil {
		return err
	}

	node := store.GetNode(deployState, name)
	if node == nil {
		return exitWithError(outputFmt, "NODE_NOT_FOUND", output.ExitGeneralError,
			fmt.Sprintf("Node %q not found", name),
			"Run: trond list")
	}

	// Build the contract-shaped response. The CLI contract
	// (specs/.../contracts/cli-contract.md) and knowledge/test-harness.md
	// promise block_height, peer_count, sync_progress_percent, is_synced,
	// uptime, api_endpoints — populate them when reachable, leave the
	// keys absent when the API isn't (so JSON consumers can distinguish
	// "not yet healthy" from "I forgot the field").
	statusInfo := map[string]any{
		"name":         node.Name,
		"status":       node.Status,
		"runtime":      node.Runtime,
		"version":      node.Version,
		"target":       node.Target,
		"last_applied": node.LastApplied,
		"intent_hash":  node.IntentHash,
		"config_hash":  node.ConfigHash,
		"api_endpoints": map[string]any{
			"http": fmt.Sprintf("http://127.0.0.1:%d", effectivePort(node.HTTPPort, 8090)),
			"grpc": fmt.Sprintf("127.0.0.1:%d", effectivePort(node.GRPCPort, 50051)),
		},
	}

	// Live probe — best effort, 3s timeout. We skip if the node isn't
	// running per state, since calling curl against a stopped node is
	// just noise.
	if node.Status == "running" {
		ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
		defer cancel()
		maps.Copy(statusInfo, liveStatusProbe(ctx, node))
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, statusInfo)
	}

	fmt.Printf("Node:         %s\n", node.Name)
	fmt.Printf("Status:       %s\n", node.Status)
	fmt.Printf("Runtime:      %s\n", node.Runtime)
	fmt.Printf("Version:      %s\n", node.Version)
	fmt.Printf("Target:       %s\n", node.Target.Type)
	fmt.Printf("Last Applied: %s\n", node.LastApplied.Format("2006-01-02 15:04:05 UTC"))
	if h, ok := statusInfo["block_height"].(int64); ok {
		fmt.Printf("Block height: %d\n", h)
	}
	if p, ok := statusInfo["peer_count"].(int); ok {
		fmt.Printf("Peers:        %d\n", p)
	}
	if syn, ok := statusInfo["is_synced"].(bool); ok {
		fmt.Printf("Synced:       %v\n", syn)
	}
	return nil
}

func effectivePort(stored, fallback int) int {
	if stored != 0 {
		return stored
	}
	return fallback
}

// liveStatusProbe makes the (cheap) HTTP API calls a status command is
// expected to surface. Errors are silently dropped — the caller sees
// the keys appear or not, never a failure on the whole command.
func liveStatusProbe(ctx context.Context, node *state.ManagedNode) map[string]any {
	out := map[string]any{}
	port := effectivePort(node.HTTPPort, 8090)

	tgt, err := resolveTargetFromNode(node)
	if err != nil {
		return out
	}
	if c, ok := any(tgt).(interface{ Close() error }); ok {
		defer c.Close()
	}

	probe := func(path string) ([]byte, error) {
		url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
		// docker exec for docker runtime, host curl for jar runtime.
		// Mirrors the diagnose checkers' approach.
		if node.Runtime == "jar" {
			return tgt.Exec(ctx, "curl", "-fsS", "--max-time", "2", url)
		}
		return tgt.Exec(ctx, "docker", "exec", node.Name, "curl", "-fsS", "--max-time", "2", url)
	}

	// Block height + sync state from getnowblock.
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
				// "synced" heuristic: tip is within 60s of now. Good enough for
				// dashboards; not a consensus-level claim.
				lag := time.Since(time.UnixMilli(block.BlockHeader.RawData.Timestamp))
				out["is_synced"] = lag < 60*time.Second
			}
		}
	}

	// Peer count from listnodes.
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
