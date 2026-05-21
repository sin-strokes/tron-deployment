package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/build"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// `trond build list` enumerates the on-disk build cache so operators
// (and agents via `--output json`) can see what artifacts are taking
// space, when they were built, and from which source revision —
// without poking under the state directory by hand.
//
// Default behavior hides orphans (manifests whose artifact has been
// deleted out-of-band). `--include-orphans` surfaces them too, which
// is the data shape `trond build prune --orphan` operates on.
//
// Output schema: schemas/output/build-list.schema.json.

var (
	buildListFilter         string
	buildListSort           string
	buildListIncludeOrphans bool
)

var buildListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cached build artifacts",
	Long: `Walk the trond build cache directory and emit one row per
cached artifact (JAR or image). Useful for finding the cache key to
reference in trond build inspect / prune, or for spotting orphaned
manifests whose underlying artifact has been deleted out-of-band.`,
	Example: `  # Table view, newest first.
  trond build list

  # Just images, JSON for piping into jq.
  trond build list --filter image -o json

  # Sort by size to find the biggest cache hogs.
  trond build list --sort size`,
	RunE: runBuildList,
}

func init() {
	buildListCmd.Flags().StringVar(&buildListFilter, "filter", "all",
		"Artifact kind to include: 'all' (default), 'jar', or 'image'")
	buildListCmd.Flags().StringVar(&buildListSort, "sort", "newest",
		"Sort order: 'newest' (default), 'oldest', or 'size' (largest first)")
	buildListCmd.Flags().BoolVar(&buildListIncludeOrphans, "include-orphans", false,
		"Include cache entries whose underlying artifact is missing")
	buildCmd.AddCommand(buildListCmd)
}

func runBuildList(cmd *cobra.Command, _ []string) error {
	opts := []build.ListOption{}
	if buildListIncludeOrphans {
		opts = append(opts, build.IncludeOrphans())
	}
	entries, err := build.ListEntries(cmd.Context(), opts...)
	if err != nil {
		return output.NewErrorf("LIST_ERROR", output.ExitGeneralError,
			"list build cache: %s", err.Error())
	}

	// Apply --filter and --sort here at the CLI layer so the
	// library's ListEntries stays a thin walker. (--filter is
	// post-walk: walking is cheap, image-size lookups already
	// happened; filtering after is straightforward.)
	entries = filterEntriesByKind(entries, buildListFilter)
	if err := sortEntries(entries, buildListSort); err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	outputFmt, _ := cmd.Flags().GetString("output")
	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"entries": entries,
			"count":   len(entries),
		})
	}
	return printBuildListTable(entries)
}

func filterEntriesByKind(entries []*build.Entry, filter string) []*build.Entry {
	if filter == "" || filter == "all" {
		return entries
	}
	out := make([]*build.Entry, 0, len(entries))
	for _, e := range entries {
		if e.ArtifactKind == filter {
			out = append(out, e)
		}
	}
	return out
}

func sortEntries(entries []*build.Entry, order string) error {
	switch order {
	case "", "newest":
		// ListEntries already returned newest-first; no-op.
		return nil
	case "oldest":
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].CreatedAt.Before(entries[j].CreatedAt)
		})
		return nil
	case "size":
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].SizeBytes > entries[j].SizeBytes
		})
		return nil
	default:
		return fmt.Errorf("invalid --sort %q (want: newest|oldest|size)", order)
	}
}

func printBuildListTable(entries []*build.Entry) error {
	if len(entries) == 0 {
		fmt.Println("No cached builds.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CACHE_KEY\tKIND\tSOURCE_REV\tSIZE\tCREATED\tARTIFACT")
	for _, e := range entries {
		artifact := e.ImageTag
		if e.ArtifactKind == "jar" {
			artifact = e.ArtifactPath
		}
		marker := ""
		if e.Orphaned {
			marker = " (orphan)"
		}
		fmt.Fprintf(tw, "%s\t%s%s\t%s\t%s\t%s\t%s\n",
			e.CacheKey,
			e.ArtifactKind, marker,
			shortRev(e.SourceRevision),
			humanBytes(e.SizeBytes),
			e.CreatedAt.Local().Format("2006-01-02 15:04"),
			artifact,
		)
	}
	return tw.Flush()
}

// shortRev trims a 40-char git sha to 12 chars for table readability.
func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// humanBytes formats a byte count as a short string (e.g. "615MB").
// Operators don't need bytes precision in a table — JSON output
// still carries the raw size_bytes for tooling.
func humanBytes(n int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1fGB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%dMB", n/mib)
	case n >= kib:
		return fmt.Sprintf("%dKB", n/kib)
	case n == 0:
		return "-"
	default:
		return fmt.Sprintf("%dB", n)
	}
}
