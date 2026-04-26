package network

import (
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/runtime"
	"github.com/tronprotocol/tron-deployment/internal/state"
	"github.com/tronprotocol/tron-deployment/internal/target"
)

var destroyConfirm string

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy a private network",
	Long:  "Stop and remove all nodes belonging to a network.",
	RunE:  runDestroy,
}

func init() {
	destroyCmd.Flags().StringVar(&destroyConfirm, "confirm", "", "Confirm by repeating the network name")
}

func runDestroy(cmd *cobra.Command, args []string) error {
	_, _ = cmd.Flags().GetString("output")
	start := time.Now()

	if destroyConfirm == "" {
		return output.NewError("HUMAN_REQUIRED", output.ExitHumanRequired,
			"Destructive operation: destroying network").
			WithSuggestions("Add --confirm <network-name> to proceed")
	}

	store, err := state.NewStore(paths.State())
	if err != nil {
		return err
	}

	deployState, err := store.Load()
	if err != nil {
		return err
	}

	// Pre-flight: refuse to "destroy" a name that owns zero nodes — almost
	// always a typo (e.g. `--confirm=wrng` instead of the real network).
	// Returning success on a no-op silently masks the mistake.
	prefix := destroyConfirm + "-node"
	matchesAny := false
	for _, n := range deployState.Nodes {
		if strings.HasPrefix(n.Name, prefix) || n.Name == destroyConfirm {
			matchesAny = true
			break
		}
	}
	if !matchesAny {
		return output.NewError("NETWORK_NOT_FOUND", output.ExitGeneralError,
			"no network named "+destroyConfirm+" — nothing to destroy").
			WithSuggestions("Run: trond network status",
				"Run: trond list  (to see all managed nodes)")
	}

	workDir := paths.Deployments()
	tgt := target.NewLocalTarget()

	var removed []string

	// Find and remove all network nodes
	for i := len(deployState.Nodes) - 1; i >= 0; i-- {
		n := deployState.Nodes[i]
		if strings.HasPrefix(n.Name, prefix) || n.Name == destroyConfirm {
			rt := runtime.NewDockerRuntime(tgt, workDir)
			rt.Remove(cmd.Context(), n.Name, true)
			removed = append(removed, n.Name)
			store.RemoveNode(deployState, n.Name)
		}
	}

	store.Save(deployState)
	writeAudit(auditEvent{Command: "network-destroy", Node: destroyConfirm, Target: "local", Result: "success", Start: start})

	result := map[string]any{
		"network": destroyConfirm,
		"removed": removed,
	}
	output.WriteJSON(os.Stdout, result)
	return nil
}
