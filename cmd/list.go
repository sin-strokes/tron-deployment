package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var listLabelFlags []string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all managed nodes",
	Long: `List managed nodes, optionally filtered by label.

  trond list --label role=api          # only api nodes
  trond list --label role=api --label tier=edge   # AND across flags`,
	RunE: runList,
}

func init() {
	listCmd.Flags().StringArrayVar(&listLabelFlags, "label", nil, "Filter by intent label (key=value, repeatable; AND semantics)")
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

	filter, err := parseLabelFilter(listLabelFlags)
	if err != nil {
		return err
	}
	nodes := deployState.Nodes
	if filter != nil {
		filtered := make([]state.ManagedNode, 0, len(nodes))
		for i := range nodes {
			if matchesLabels(&nodes[i], filter) {
				filtered = append(filtered, nodes[i])
			}
		}
		nodes = filtered
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, nodes)
	}

	if len(nodes) == 0 {
		if filter != nil {
			fmt.Println("No managed nodes match the given --label filter.")
		} else {
			fmt.Println("No managed nodes. Deploy one with: trond apply --intent <file>")
		}
		return nil
	}

	fmt.Printf("%-20s %-10s %-10s %-10s %s\n", "NAME", "STATUS", "RUNTIME", "NETWORK", "VERSION")
	for _, n := range nodes {
		fmt.Printf("%-20s %-10s %-10s %-10s %s\n",
			n.Name, n.Status, n.Runtime, n.Target.Type, n.Version)
	}

	return nil
}
