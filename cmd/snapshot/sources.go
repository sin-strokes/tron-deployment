package snapshot

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
)

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "List known snapshot mirrors",
	Long: `Print every snapshot source trond knows about, grouped by network and
db kind (lite vs full). Pick one with --domain on download, or let
trond pick a default by passing --network and --type.`,
	RunE: runSources,
}

func runSources(cmd *cobra.Command, _ []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")
	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"sources": snapshot.SourceTable,
		})
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NETWORK\tKIND\tENGINE\tREGION\tDOMAIN\t~SIZE\tNOTES")
	for _, s := range snapshot.SourceTable {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%dG\t%s\n",
			s.Network, s.DBKind, s.DBEngine, s.Region, s.Domain, s.ApproxSizeGB, s.Description)
	}
	return tw.Flush()
}
