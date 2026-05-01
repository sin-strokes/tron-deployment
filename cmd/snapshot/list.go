package snapshot

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
)

var (
	listNetwork string
	listDomain  string
	listKind    string
	listRegion  string
	listEngine  string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available backups for a snapshot source",
	Long: `Show the backup names available at a chosen mirror, newest-first.
You can resolve a source by --domain, or let trond pick by --network /
--type / --region.`,
	Example: `  trond snapshot list --network mainnet
  trond snapshot list --network nile
  trond snapshot list --domain 34.143.247.77`,
	RunE: runList,
}

func init() {
	listCmd.Flags().StringVar(&listNetwork, "network", "", "Network: mainnet | nile")
	listCmd.Flags().StringVar(&listDomain, "domain", "", "Mirror domain (overrides --network/--region)")
	listCmd.Flags().StringVar(&listKind, "type", "lite", "Snapshot kind: lite | full")
	listCmd.Flags().StringVar(&listRegion, "region", "", "Region: singapore | america")
	listCmd.Flags().StringVar(&listEngine, "db-engine", "", "Engine: leveldb | rocksdb (mainnet full only)")
}

func runList(cmd *cobra.Command, _ []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	src, err := resolveSource(listDomain, listNetwork, listKind, listRegion, listEngine)
	if err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	backups, err := snapshot.ListBackups(cmd.Context(), *src)
	if err != nil {
		return output.NewError("LIST_ERROR", output.ExitGeneralError, err.Error())
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"domain":  src.Domain,
			"network": src.Network,
			"kind":    src.DBKind,
			"backups": backups,
		})
	}

	fmt.Printf("Source: %s (%s, %s, %s)\n", src.Domain, src.Network, src.DBKind, src.Region)
	fmt.Printf("Backups (%d, newest first):\n", len(backups))
	for _, b := range backups {
		fmt.Printf("  %s\n", b)
	}
	return nil
}

// resolveSource collapses --domain / --network / --type / --region / --db-engine
// into a single Source, with friendly errors when the combination doesn't
// match anything in the table.
func resolveSource(domain, network, kind, region, engine string) (*snapshot.Source, error) {
	if domain != "" {
		s := snapshot.LookupDomain(domain)
		if s == nil {
			return nil, fmt.Errorf("unknown domain %q (try `trond snapshot sources`)", domain)
		}
		return s, nil
	}
	if network == "" {
		return nil, fmt.Errorf("must pass --network or --domain (try `trond snapshot sources`)")
	}
	f := snapshot.Filter{
		Network:  snapshot.Network(network),
		DBKind:   snapshot.DBKind(kind),
		Region:   snapshot.Region(region),
		DBEngine: snapshot.DBEngine(engine),
	}
	s := snapshot.Pick(f)
	if s == nil {
		return nil, fmt.Errorf("no source matches network=%s type=%s region=%s engine=%s",
			network, kind, region, engine)
	}
	return s, nil
}
