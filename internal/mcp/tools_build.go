package mcp

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tronprotocol/tron-deployment/internal/build"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// registerBuildTools exposes the build pipeline's cache-management
// commands as MCP tools so chat-based agents can survey and clean
// up the build cache without shelling out. Build execution itself
// (`trond build`) is intentionally not exposed here — it's a long-
// running process whose progress is best surfaced via the CLI's
// dedicated --output json stream; agents that need to drive a build
// should call the CLI binary directly. Phase 5 follow-up may add a
// build_run tool with MCP progress notifications.

// build_list args. Mirrors `trond build list` flags.
type buildListArgs struct {
	Filter         string `json:"filter,omitempty" jsonschema:"artifact kind: all (default) | jar | image"`
	Sort           string `json:"sort,omitempty" jsonschema:"order: newest (default) | oldest | size"`
	IncludeOrphans bool   `json:"include_orphans,omitempty" jsonschema:"include entries whose underlying artifact is missing"`
}

// build_inspect args.
type buildInspectArgs struct {
	CacheKey string `json:"cache_key" jsonschema:"full cache key or unambiguous prefix (e.g. '260585c9397b' instead of the full 22+-char form)"`
}

// build_prune args. Mirrors `trond build prune` flags. confirm
// defaults to false so a careless invocation produces a dry-run
// plan, never a destructive deletion.
type buildPruneArgs struct {
	All        bool   `json:"all,omitempty" jsonschema:"remove every cached build (requires confirm)"`
	OlderThan  string `json:"older_than,omitempty" jsonschema:"only consider entries older than this Go duration (e.g. '24h', '168h'); empty disables the filter"`
	KeepLast   int    `json:"keep_last,omitempty" jsonschema:"protect the N newest entries from pruning regardless of other filters"`
	OrphanOnly bool   `json:"orphan_only,omitempty" jsonschema:"only consider entries whose underlying artifact is missing"`
	Confirm    bool   `json:"confirm,omitempty" jsonschema:"actually perform deletions (omit for a dry-run plan)"`
}

func registerBuildTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "build_list",
		Title:       "List cached build artifacts",
		Description: "Walk the trond build cache directory and return every cached artifact (JAR or image) with its size, source revision, and orphan state. Read-only. Equivalent to `trond build list -o json`.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, buildListTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "build_inspect",
		Title:       "Inspect one cached build",
		Description: "Return the full manifest plus computed artifact size and orphan state for a single cache entry. Accepts either the full cache key or an unambiguous prefix. Read-only. Equivalent to `trond build inspect <cache-key> -o json`.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, buildInspectTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "build_prune",
		Title: "Prune the build cache",
		Description: `Remove cached build artifacts per a deletion policy. Filters AND together: keep_last N protects the N newest entries globally; orphan_only restricts to entries whose artifact is gone; older_than restricts to entries created before now-duration; all wipes everything (overrides other filters). For image entries, also runs a best-effort 'docker image rm --force <tag>' so docker's storage actually reclaims layers.

Defaults to dry-run (no confirm). The result's 'plan' field shows what WOULD be removed; 'removed' is populated only on confirm=true. Equivalent to 'trond build prune ... -o json'.`,
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: ptrTrue(),
		},
	}, buildPruneTool)
}

func buildListTool(ctx context.Context, _ *mcp.CallToolRequest, args buildListArgs) (*mcp.CallToolResult, any, error) {
	opts := []build.ListOption{}
	if args.IncludeOrphans {
		opts = append(opts, build.IncludeOrphans())
	}
	entries, err := build.ListEntries(ctx, opts...)
	if err != nil {
		return errResult(output.NewErrorf("LIST_ERROR", output.ExitGeneralError,
			"list build cache: %s", err.Error()))
	}

	entries = filterMCPEntries(entries, args.Filter)
	if sortErr := sortMCPEntries(entries, args.Sort); sortErr != nil {
		return errResult(output.NewError("VALIDATION_ERROR", output.ExitValidationError, sortErr.Error()))
	}

	return jsonResult(map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

func buildInspectTool(ctx context.Context, _ *mcp.CallToolRequest, args buildInspectArgs) (*mcp.CallToolResult, any, error) {
	entry, err := build.InspectEntry(ctx, args.CacheKey)
	if err != nil {
		switch {
		case errors.Is(err, build.ErrNoMatch):
			return errResult(output.NewErrorf("NOT_FOUND", output.ExitGeneralError,
				"no cache entry matches %q", args.CacheKey).
				WithSuggestions("Call build_list to see available cache keys"))
		case errors.Is(err, build.ErrAmbiguousPrefix):
			return errResult(output.NewErrorf("AMBIGUOUS_PREFIX", output.ExitValidationError,
				"%s", err.Error()).
				WithSuggestions("Re-run with a longer prefix or the full cache key"))
		default:
			return errResult(output.NewErrorf("INSPECT_ERROR", output.ExitGeneralError,
				"inspect cache entry: %s", err.Error()))
		}
	}
	return jsonResult(entry)
}

func buildPruneTool(ctx context.Context, _ *mcp.CallToolRequest, args buildPruneArgs) (*mcp.CallToolResult, any, error) {
	// Same friendly guard as the CLI: empty policy is almost always
	// an LLM mistake, not an intent to no-op.
	if !args.All && !args.OrphanOnly && args.OlderThan == "" && args.KeepLast == 0 {
		return errResult(output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"prune needs at least one of: all, orphan_only, older_than, keep_last").
			WithSuggestions(
				"To wipe everything: build_prune with all=true confirm=true",
				"To remove orphans only: build_prune with orphan_only=true confirm=true",
				"To remove entries older than a week: build_prune with older_than='168h' confirm=true",
			))
	}
	// Footgun guard, MCP variant of the CLI's same check: keep_last
	// alone + confirm=true deletes every entry except the N newest,
	// which is a near-wipe an LLM might invoke under "trim cache
	// down to recent". Require either all=true (explicit acknowledge)
	// or a scoping filter (orphan_only / older_than).
	if args.Confirm && args.KeepLast > 0 &&
		!args.All && !args.OrphanOnly && args.OlderThan == "" {
		return errResult(output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"keep_last alone with confirm=true would wipe everything except "+
				"the N newest entries; set all=true to acknowledge, OR "+
				"narrow with orphan_only / older_than").
			WithSuggestions(
				"Preview first: omit confirm=true (the plan shows exactly what would be removed)",
				"To genuinely wipe-all-but-N: all=true keep_last=N confirm=true",
			))
	}

	var olderThan time.Duration
	if args.OlderThan != "" {
		d, err := time.ParseDuration(args.OlderThan)
		if err != nil {
			return errResult(output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
				"older_than %q is not a valid Go duration: %s", args.OlderThan, err.Error()).
				WithSuggestions("Use Go duration syntax: '24h', '168h' (= 7 days), '720h' (= 30 days)"))
		}
		olderThan = d
	}

	res, err := build.Prune(ctx, build.PruneOptions{
		All:        args.All,
		OlderThan:  olderThan,
		KeepLast:   args.KeepLast,
		OrphanOnly: args.OrphanOnly,
		DryRun:     !args.Confirm,
	})
	if err != nil {
		return errResult(output.NewErrorf("PRUNE_ERROR", output.ExitGeneralError,
			"prune build cache: %s", err.Error()))
	}
	return jsonResult(res)
}

// filterMCPEntries mirrors cmd/build_list.go's filterEntriesByKind.
// Duplicated rather than imported because internal/mcp must not
// depend on cmd/.
func filterMCPEntries(entries []*build.Entry, filter string) []*build.Entry {
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

// sortMCPEntries mirrors cmd/build_list.go's sortEntries.
func sortMCPEntries(entries []*build.Entry, order string) error {
	switch order {
	case "", "newest":
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
		return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"invalid sort %q (want: newest|oldest|size)", order)
	}
}
