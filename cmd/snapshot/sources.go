package snapshot

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
)

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "List or probe known snapshot mirrors",
	Long: `Print every snapshot source trond knows about, grouped by network and
db kind (lite vs full). Pick one with --domain on download, or let
trond pick a default by passing --network and --type.

Pass --probe to additionally HEAD-check each mirror and report which
ones still serve recent backups. Useful for CI to catch upstream
mirror rotations before users do — Task #161's structural follow-up.`,
	RunE: runSources,
}

func init() {
	sourcesCmd.Flags().Bool("probe", false, "HEAD-check every source and report reachability + freshness")
	sourcesCmd.Flags().Duration("probe-timeout", 8*time.Second, "per-HEAD HTTP timeout when probing")
	sourcesCmd.Flags().Duration("stale-after", 7*24*time.Hour, "age beyond which a reachable backup is reported as 'stale'")
	sourcesCmd.Flags().Int("probe-parallelism", 5, "max concurrent HEAD checks during --probe")
}

func runSources(cmd *cobra.Command, _ []string) error {
	probe, _ := cmd.Flags().GetBool("probe")
	outputFmt, _ := cmd.Flags().GetString("output")

	if !probe {
		return printSourceTable(outputFmt)
	}

	timeout, _ := cmd.Flags().GetDuration("probe-timeout")
	stale, _ := cmd.Flags().GetDuration("stale-after")
	parallelism, _ := cmd.Flags().GetInt("probe-parallelism")
	return runProbe(cmd.Context(), outputFmt, snapshot.ProbeOptions{
		HTTPTimeout: timeout,
		StaleAfter:  stale,
	}, parallelism)
}

func printSourceTable(outputFmt string) error {
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

// runProbe walks SourceTable, HEAD-checks each, and prints results.
// Returns a non-nil error when any source is not ProbeOK so a CI step
// fails cleanly (exit code 1) without us hand-rolling os.Exit. JSON
// output still prints the full report before erroring.
func runProbe(ctx context.Context, outputFmt string, opts snapshot.ProbeOptions, parallelism int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	results := snapshot.ProbeAll(ctx, snapshot.SourceTable, opts, parallelism)

	summary := map[snapshot.ProbeStatus]int{}
	for _, r := range results {
		summary[r.Status]++
	}

	if outputFmt == "json" {
		_ = output.WriteJSON(os.Stdout, map[string]any{
			"probed_at": time.Now().UTC().Format(time.RFC3339),
			"results":   results,
			"summary":   summary,
		})
	} else {
		printProbeTable(results, summary)
	}

	if summary[snapshot.ProbeOK] != len(results) {
		return fmt.Errorf("%d/%d sources not OK (stale=%d unreachable=%d no_backups=%d bad_config=%d)",
			len(results)-summary[snapshot.ProbeOK], len(results),
			summary[snapshot.ProbeStale], summary[snapshot.ProbeUnreachable],
			summary[snapshot.ProbeNoBackups], summary[snapshot.ProbeBadConfig])
	}
	return nil
}

func printProbeTable(results []snapshot.ProbeResult, summary map[snapshot.ProbeStatus]int) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tNETWORK\tKIND\tENGINE\tDOMAIN\tLATEST\tAGE\tLATENCY\tDETAIL")
	for _, r := range results {
		latest := r.LatestBackup
		if latest == "" {
			latest = "-"
		}
		age := "-"
		if r.LatestAgeDays > 0 {
			age = fmt.Sprintf("%dd", r.LatestAgeDays)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%dms\t%s\n",
			r.Status, r.Source.Network, r.Source.DBKind, r.Source.DBEngine,
			r.Source.Domain, latest, age, r.LatencyMs, r.Err)
	}
	_ = tw.Flush()
	fmt.Printf("\nsummary: ok=%d stale=%d unreachable=%d no_backups=%d bad_config=%d\n",
		summary[snapshot.ProbeOK], summary[snapshot.ProbeStale],
		summary[snapshot.ProbeUnreachable], summary[snapshot.ProbeNoBackups],
		summary[snapshot.ProbeBadConfig])
}
