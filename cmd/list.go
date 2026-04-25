package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all managed nodes",
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	store, err := state.NewStore(statePath())
	if err != nil {
		return err
	}

	deployState, err := store.Load()
	if err != nil {
		return err
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, deployState.Nodes)
	}

	if len(deployState.Nodes) == 0 {
		fmt.Println("No managed nodes. Deploy one with: trond apply --intent <file>")
		return nil
	}

	fmt.Printf("%-20s %-10s %-10s %-10s %s\n", "NAME", "STATUS", "RUNTIME", "NETWORK", "VERSION")
	for _, n := range deployState.Nodes {
		fmt.Printf("%-20s %-10s %-10s %-10s %s\n",
			n.Name, n.Status, n.Runtime, n.Target.Type, n.Version)
	}

	return nil
}
