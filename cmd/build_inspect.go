package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/build"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// `trond build inspect <cache-key>` returns the full manifest plus
// the computed artifact size + orphan flag for a single cache entry.
// Designed for the agent workflow where `trond build list -o json`
// returned a cache key and the agent now wants the full record.
//
// The cache key may be a full key (e.g. `260585c9397b-bd0861e68+...`)
// or an unambiguous prefix (e.g. `260585`). Ambiguous prefixes
// return AMBIGUOUS_PREFIX with the candidate list in the message so
// the operator can disambiguate.
//
// Output schema: schemas/output/build-inspect.schema.json.

var buildInspectCmd = &cobra.Command{
	Use:   "inspect <cache-key>",
	Short: "Show full details for one cached build",
	Long: `Look up a cached build by its cache key (or an unambiguous prefix)
and print the full manifest plus the computed artifact size and orphan
state. Mirrors the per-entry shape produced by 'trond build list -o
json' so downstream tools can parse either output identically.`,
	Example: `  # Full key.
  trond build inspect 260585c9397b-bd0861e68+dirty-a71dd635-x25f78389

  # Unambiguous prefix.
  trond build inspect 260585c9 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runBuildInspect,
}

func init() {
	buildCmd.AddCommand(buildInspectCmd)
}

func runBuildInspect(cmd *cobra.Command, args []string) error {
	entry, err := build.InspectEntry(cmd.Context(), args[0])
	if err != nil {
		switch {
		case errors.Is(err, build.ErrNoMatch):
			return output.NewErrorf("NOT_FOUND", output.ExitGeneralError,
				"no cache entry matches %q", args[0]).
				WithSuggestions("Run 'trond build list' to see available cache keys")
		case errors.Is(err, build.ErrAmbiguousPrefix):
			return output.NewErrorf("AMBIGUOUS_PREFIX", output.ExitValidationError,
				"%s", err.Error()).
				WithSuggestions("Re-run with a longer prefix or the full cache key")
		default:
			return output.NewErrorf("INSPECT_ERROR", output.ExitGeneralError,
				"inspect cache entry: %s", err.Error())
		}
	}

	outputFmt, _ := cmd.Flags().GetString("output")
	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, entry)
	}
	printInspectText(entry)
	return nil
}

func printInspectText(e *build.Entry) {
	fmt.Printf("cache_key:       %s\n", e.CacheKey)
	if e.Orphaned {
		fmt.Println("status:          ORPHAN (artifact missing on disk)")
	} else {
		fmt.Println("status:          OK")
	}
	fmt.Printf("artifact_kind:   %s\n", e.ArtifactKind)
	if e.ArtifactKind == "jar" {
		fmt.Printf("artifact_path:   %s\n", e.ArtifactPath)
		fmt.Printf("sha256:          %s\n", e.SHA256)
	} else {
		fmt.Printf("image_tag:       %s\n", e.ImageTag)
		fmt.Printf("image_id:        %s\n", e.ImageID)
	}
	fmt.Printf("size:            %s\n", humanBytes(e.SizeBytes))
	fmt.Printf("source_path:     %s\n", e.SourcePath)
	fmt.Printf("source_revision: %s\n", e.SourceRevision)
	if e.Dirty {
		fmt.Printf("patch_hash:      %s (dirty tree)\n", e.PatchHash)
	}
	fmt.Printf("builder:         %s\n", e.Builder)
	fmt.Printf("builder_image:   %s\n", e.BuilderImage)
	fmt.Printf("jdk_version:     %s\n", e.JDKVersion)
	fmt.Printf("platform:        %s\n", e.Platform)
	fmt.Printf("gradle_task:     %s\n", e.GradleTask)
	if len(e.GradleArgs) > 0 {
		fmt.Printf("gradle_args:     %v\n", e.GradleArgs)
	}
	fmt.Printf("duration_ms:     %d\n", e.DurationMs)
	fmt.Printf("created_at:      %s\n", e.CreatedAt.Local().Format("2006-01-02 15:04:05 MST"))
}
