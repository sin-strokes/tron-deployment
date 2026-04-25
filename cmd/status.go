package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var statusCmd = &cobra.Command{
	Use:   "status [node]",
	Short: "Show node status (or list all nodes)",
	Long:  "Without arguments: list all managed nodes. With a node name: show detailed status.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runStatus,
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

	statusInfo := map[string]any{
		"name":         node.Name,
		"status":       node.Status,
		"runtime":      node.Runtime,
		"version":      node.Version,
		"target":       node.Target,
		"last_applied": node.LastApplied,
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

	return nil
}
