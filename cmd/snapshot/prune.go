package snapshot

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
)

// pruneCmd reclaims disk space taken by stopped snapshot jobs that
// nobody is going to look at again. By default it removes only jobs
// that:
//
//   - Are no longer running (kill(0) on the pid fails)
//   - Older than --older-than (default 7 days)
//
// It never touches running jobs unless --running is also set, and
// even then we require an explicit --confirm to avoid an accidental
// pkill from someone who just typed `trond snapshot prune --running`
// and walked away.
var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove old stopped snapshot jobs from ~/.trond/snapshots/",
	Long: `Reclaim disk space taken by snapshot job manifests + log files left
behind by previous downloads. Default policy:

  * stopped jobs only (running ones are preserved)
  * older than --older-than (default 7 days, measured by job's
    StartedAt timestamp)

Use --dry-run to preview what would be removed. The output is the
list of job ids and the bytes reclaimed.`,
	Example: `  trond snapshot prune                          # default: stopped jobs older than 7d
  trond snapshot prune --older-than 24h --dry-run
  trond snapshot prune --older-than 0 --all     # everything stopped, no age filter`,
	RunE: runPrune,
}

var (
	pruneOlderThan time.Duration
	pruneDryRun    bool
	pruneAll       bool
)

func init() {
	pruneCmd.Flags().DurationVar(&pruneOlderThan, "older-than", 7*24*time.Hour,
		"Only prune jobs whose StartedAt is older than this duration (set 0 to disable the age filter)")
	pruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false,
		"Print which jobs would be pruned without removing anything")
	pruneCmd.Flags().BoolVar(&pruneAll, "all", false,
		"Disable the age filter (same as --older-than 0); convenient shortcut")
}

func runPrune(cmd *cobra.Command, _ []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	jobsDir := paths.SnapshotJobs()
	jobs, err := snapshot.ListJobs(jobsDir)
	if err != nil {
		return output.NewError("PRUNE_ERROR", output.ExitGeneralError, err.Error())
	}

	cutoff := time.Now().Add(-pruneOlderThan)
	if pruneAll {
		// --all = no age cutoff at all (cutoff in the future = always older).
		cutoff = time.Now().Add(24 * time.Hour)
	}

	type pruneItem struct {
		ID         string `json:"id"`
		PID        int    `json:"pid"`
		StartedAt  string `json:"started_at"`
		LogBytes   int64  `json:"log_bytes"`
		Removed    bool   `json:"removed"`
		SkipReason string `json:"skip_reason,omitempty"`
	}

	var items []pruneItem
	var totalBytes int64
	var removedCount int

	for _, j := range jobs {
		st := snapshot.Status(jobsDir, j)
		item := pruneItem{
			ID:        j.ID,
			PID:       j.PID,
			StartedAt: j.StartedAt.UTC().Format(time.RFC3339),
			LogBytes:  st.LogSize,
		}
		switch {
		case st.Running:
			item.SkipReason = "running"
		case j.StartedAt.After(cutoff):
			item.SkipReason = "younger than --older-than"
		default:
			if pruneDryRun {
				item.Removed = false
				item.SkipReason = "dry-run"
			} else if err := snapshot.RemoveJob(jobsDir, j.ID); err != nil {
				item.SkipReason = "remove failed: " + err.Error()
			} else {
				item.Removed = true
				totalBytes += st.LogSize
				removedCount++
			}
		}
		items = append(items, item)
	}

	result := map[string]any{
		"jobs":            items,
		"removed_count":   removedCount,
		"reclaimed_bytes": totalBytes,
		"dry_run":         pruneDryRun,
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, result)
	}

	if removedCount == 0 && !pruneDryRun {
		fmt.Println("No jobs eligible for pruning.")
	}
	for _, it := range items {
		state := "kept"
		if it.Removed {
			state = "removed"
		} else if it.SkipReason == "dry-run" {
			state = "would-remove"
		}
		fmt.Printf("  %-22s %-12s %s\n", it.ID, state, it.SkipReason)
	}
	if pruneDryRun {
		dryCount, dryBytes := 0, int64(0)
		for _, it := range items {
			if it.SkipReason == "dry-run" {
				dryCount++
				dryBytes += it.LogBytes
			}
		}
		if dryCount > 0 {
			fmt.Printf("\nWould reclaim ~%s across %d job(s).\n", humanGB(uint64(dryBytes)), dryCount)
		}
	} else if removedCount > 0 {
		fmt.Printf("\nReclaimed ~%s across %d job(s).\n", humanGB(uint64(totalBytes)), removedCount)
	}
	return nil
}
