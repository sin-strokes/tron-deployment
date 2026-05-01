package network

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all network nodes",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	store, err := state.NewStore(paths.State())
	if err != nil {
		return err
	}

	deployState, err := store.Load()
	if err != nil {
		return err
	}

	// Filter for network nodes (name contains "-node"). Always emit a slice
	// (never nil) so JSON consumers can rely on the array shape.
	networkNodes := make([]state.ManagedNode, 0, len(deployState.Nodes))
	for _, n := range deployState.Nodes {
		if strings.Contains(n.Name, "-node") {
			networkNodes = append(networkNodes, n)
		}
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, networkNodes)
	}

	if len(networkNodes) == 0 {
		fmt.Println("No network nodes found.")
		return nil
	}

	fmt.Printf("%-25s %-10s %-10s %s\n", "NAME", "STATUS", "RUNTIME", "VERSION")
	for _, n := range networkNodes {
		fmt.Printf("%-25s %-10s %-10s %s\n", n.Name, n.Status, n.Runtime, n.Version)
	}

	return nil
}
