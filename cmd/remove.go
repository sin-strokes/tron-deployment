package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
)

var (
	removeKeepData bool
	removeConfirm  string
)

var removeCmd = &cobra.Command{
	Use:   "remove <node>",
	Short: "Remove a deployed node",
	Long:  "Stop and remove a deployed node. Use --confirm <name> to confirm destructive removal.",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemove,
}

func init() {
	removeCmd.Flags().BoolVar(&removeKeepData, "keep-data", false, "Keep data volumes/directories")
	removeCmd.Flags().StringVar(&removeConfirm, "confirm", "", "Confirm removal by repeating the node name")
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Require confirmation
	if removeConfirm != name {
		return exitWithError("HUMAN_REQUIRED", output.ExitHumanRequired,
			fmt.Sprintf("Destructive operation: removing node %q", name),
			fmt.Sprintf("Confirm with: trond remove %s --confirm %s", name, name))
	}

	start := time.Now()
	nc, err := resolveNodeContext(name)
	if err != nil {
		return err
	}
	defer nc.Close()

	purge := !removeKeepData
	if err := nc.Runtime.Remove(cmd.Context(), name, purge); err != nil {
		writeAudit(auditEvent{Command: "remove", Node: name, Target: nc.Target.String(), Result: "error", ErrorCode: "REMOVE_ERROR", Start: start})
		return exitWithError("REMOVE_ERROR", output.ExitGeneralError,
			fmt.Sprintf("Failed to remove %s: %v", name, err))
	}

	nc.Store.RemoveNode(nc.State, name)
	nc.Store.Save(nc.State)
	writeAudit(auditEvent{Command: "remove", Node: name, Target: nc.Target.String(), Result: "success", Start: start})

	writeResult(map[string]any{
		"name":      name,
		"status":    "removed",
		"keep_data": removeKeepData,
	})
	return nil
}
