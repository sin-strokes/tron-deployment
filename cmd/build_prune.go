package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/build"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// `trond build prune` removes cached build artifacts (JARs +
// images) per a deletion policy. Image entries also issue a
// best-effort `docker image rm --force <tag>` so docker's storage
// layer actually reclaims the bytes.
//
// Safety model:
//   - Default mode is dry-run (no --confirm). Operators ALWAYS see
//     the plan before any deletion.
//   - --confirm is required to perform deletions.
//   - --all wipes everything; pair with --confirm for that to do
//     anything.
//
// The filters AND together on the non-protected set:
//   - --keep-last N protects the N newest entries (global, not
//     per-filter) — `--older-than 1h --keep-last 3` still keeps
//     the 3 most recent builds even if all three are >1h old.
//   - --orphan restricts to entries whose artifact is gone.
//   - --older-than DUR restricts to entries created before now-DUR.
//
// Output schema: schemas/output/build-prune.schema.json.

var (
	buildPruneAll       bool
	buildPruneOlderThan time.Duration
	buildPruneKeepLast  int
	buildPruneOrphan    bool
	buildPruneConfirm   bool
)

var buildPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove cached build artifacts per a deletion policy",
	Long: `Prune the on-disk build cache (and the associated docker images
for image-kind entries). Defaults to dry-run: prints the deletion
plan without touching anything. Pass --confirm to actually delete.

Policies AND together on the non-protected set:
  --keep-last N      protect the N newest entries (global safety net)
  --orphan           restrict to entries whose artifact is missing
  --older-than DUR   restrict to entries older than now-DUR
  --all              wipe everything (overrides all filters; needs --confirm)`,
	Example: `  # Dry-run: show what would be removed.
  trond build prune --older-than 7d

  # Actually delete entries older than a week, keeping 3 newest.
  trond build prune --older-than 7d --keep-last 3 --confirm

  # Wipe everything.
  trond build prune --all --confirm

  # Cleanup orphans only.
  trond build prune --orphan --confirm -o json`,
	RunE: runBuildPrune,
}

func init() {
	buildPruneCmd.Flags().BoolVar(&buildPruneAll, "all", false,
		"Remove every cached build (requires --confirm)")
	buildPruneCmd.Flags().DurationVar(&buildPruneOlderThan, "older-than", 0,
		"Only consider entries older than this duration (e.g. 24h, 7d). "+
			"Note: go duration syntax — use 168h for 7 days.")
	buildPruneCmd.Flags().IntVar(&buildPruneKeepLast, "keep-last", 0,
		"Protect the N newest entries from pruning regardless of other filters")
	buildPruneCmd.Flags().BoolVar(&buildPruneOrphan, "orphan", false,
		"Only consider entries whose underlying artifact is missing")
	buildPruneCmd.Flags().BoolVar(&buildPruneConfirm, "confirm", false,
		"Actually perform deletions (omit for a dry-run plan)")
	buildCmd.AddCommand(buildPruneCmd)
}

func runBuildPrune(cmd *cobra.Command, _ []string) error {
	// Friendly guard: --all without --confirm or another filter
	// would silently report "would remove everything" — make the
	// intent obvious so an operator running it absent-mindedly
	// realizes they need --confirm.
	if !buildPruneAll && !buildPruneOrphan &&
		buildPruneOlderThan == 0 && buildPruneKeepLast == 0 {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"prune needs at least one of --all, --orphan, --older-than, --keep-last").
			WithSuggestions(
				"To wipe everything: trond build prune --all --confirm",
				"To remove orphans only: trond build prune --orphan --confirm",
				"To remove entries older than a week: trond build prune --older-than 168h --confirm",
			)
	}
	// Footgun guard: `--keep-last N --confirm` with NO other filter
	// is equivalent to "delete every entry except the N newest" —
	// a near-wipe operation that looks small at a glance. Require
	// either an explicit second filter (--orphan / --older-than)
	// to scope what gets pruned, OR an explicit --all to acknowledge
	// the near-wipe intent. Dry-run is exempt: the plan output
	// shows exactly what would be deleted, which IS the obvious
	// affordance an interactive operator wants.
	if buildPruneConfirm && buildPruneKeepLast > 0 &&
		!buildPruneAll && !buildPruneOrphan && buildPruneOlderThan == 0 {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"--keep-last alone with --confirm would wipe everything except "+
				"the N newest entries; combine with --all to acknowledge, OR "+
				"narrow with --orphan / --older-than").
			WithSuggestions(
				"Preview first: trond build prune --keep-last "+
					fmt.Sprintf("%d", buildPruneKeepLast)+" (dry-run shows the plan)",
				"To genuinely wipe-all-but-N: trond build prune --all --keep-last "+
					fmt.Sprintf("%d", buildPruneKeepLast)+" --confirm",
			)
	}

	opts := build.PruneOptions{
		All:        buildPruneAll,
		OlderThan:  buildPruneOlderThan,
		KeepLast:   buildPruneKeepLast,
		OrphanOnly: buildPruneOrphan,
		DryRun:     !buildPruneConfirm,
	}

	res, err := build.Prune(cmd.Context(), opts)
	if err != nil {
		return output.NewErrorf("PRUNE_ERROR", output.ExitGeneralError,
			"prune build cache: %s", err.Error())
	}

	outputFmt, _ := cmd.Flags().GetString("output")
	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, res)
	}
	printPruneText(res)
	return nil
}

func printPruneText(res *build.PruneResult) {
	if res.DryRun {
		if len(res.Plan) == 0 {
			fmt.Println("No cached builds match the policy. Nothing to prune.")
			return
		}
		fmt.Printf("Dry-run: would remove %d cache entries (~%s).\n",
			len(res.Plan), humanBytes(res.FreedBytes))
		for _, e := range res.Plan {
			printPruneEntry(e)
		}
		fmt.Println("\nRe-run with --confirm to actually delete.")
		return
	}

	if len(res.Removed) == 0 {
		fmt.Println("No cached builds matched the policy. Nothing removed.")
		return
	}
	fmt.Printf("Removed %d cache entries; freed ~%s.\n",
		len(res.Removed), humanBytes(res.FreedBytes))
	for _, e := range res.Removed {
		printPruneEntry(e)
	}
}

func printPruneEntry(e *build.Entry) {
	suffix := ""
	if e.Orphaned {
		suffix = " (orphan)"
	}
	artifact := e.ImageTag
	if e.ArtifactKind == "jar" {
		artifact = e.ArtifactPath
	}
	fmt.Printf("  - %s  %s%s  %s  %s\n",
		e.CacheKey,
		e.ArtifactKind, suffix,
		humanBytes(e.SizeBytes),
		artifact,
	)
}
