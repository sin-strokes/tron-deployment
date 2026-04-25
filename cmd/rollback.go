package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback <node>",
	Short: "Rollback a node to its previous version",
	Args:  cobra.ExactArgs(1),
	RunE:  runRollback,
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
}

func runRollback(cmd *cobra.Command, args []string) error {
	name := args[0]
	outputFmt, _ := cmd.Flags().GetString("output")
	start := time.Now()

	nc, err := resolveNodeContext(name, outputFmt)
	if err != nil {
		return err
	}
	defer nc.Close()

	if nc.Node.PreviousVersion == "" {
		return exitWithError(outputFmt, "ROLLBACK_ERROR", output.ExitGeneralError,
			fmt.Sprintf("No previous version recorded for %s", name),
			"Rollback is only available after an upgrade")
	}

	currentVersion := nc.Node.Version
	targetVersion := nc.Node.PreviousVersion

	// Stop current
	nc.Runtime.Stop(cmd.Context(), name)

	// Restore previous version
	nc.Node.Version = targetVersion
	nc.Node.PreviousVersion = currentVersion

	// Start
	if err := nc.Runtime.Start(cmd.Context(), name); err != nil {
		writeAudit(auditEvent{Command: "rollback", Node: name, Target: nc.Target.String(), Result: "error", ErrorCode: "ROLLBACK_ERROR", Start: start})
		return exitWithError(outputFmt, "ROLLBACK_ERROR", output.ExitGeneralError,
			fmt.Sprintf("Rollback to %s failed: %v", targetVersion, err))
	}

	nc.Node.Status = "running"
	nc.SaveState()
	writeAudit(auditEvent{Command: "rollback", Node: name, Target: nc.Target.String(), Result: "success", Start: start})

	writeResult(outputFmt, map[string]any{
		"name":             name,
		"status":           "running",
		"version":          targetVersion,
		"rolled_back_from": currentVersion,
	})
	return nil
}
