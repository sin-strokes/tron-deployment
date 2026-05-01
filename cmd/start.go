package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var startCmd = &cobra.Command{
	Use:   "start <node>",
	Short: "Start a stopped node",
	Args:  cobra.ExactArgs(1),
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	name := args[0]
	outputFmt, _ := cmd.Flags().GetString("output")
	start := time.Now()

	nc, err := resolveNodeContext(name, outputFmt)
	if err != nil {
		return err
	}
	defer nc.Close()

	if err := nc.Runtime.Start(cmd.Context(), name); err != nil {
		writeAudit(auditEvent{Command: "start", Node: name, Target: nc.Target.String(), Result: "error", ErrorCode: "START_ERROR", Start: start})
		return exitWithError(outputFmt, "START_ERROR", output.ExitGeneralError,
			fmt.Sprintf("Failed to start %s: %v", name, err))
	}

	nc.Node.Status = "running"
	nc.SaveState()
	writeAudit(auditEvent{Command: "start", Node: name, Target: nc.Target.String(), Result: "success", Start: start})

	writeResult(outputFmt, map[string]any{"name": name, "status": "running"})
	return nil
}
