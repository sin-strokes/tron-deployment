package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/render"
	"github.com/tronprotocol/tron-deployment/internal/state"
	"github.com/tronprotocol/tron-deployment/internal/target"
)

// registerDriftTools wires the verify_config tool — the MCP-side
// equivalent of `trond verify-config`. Agents use it as a cheap
// reconcile-or-not signal: read in_sync, decide whether to invoke
// the (destructive) apply tool.

type verifyConfigArgs struct {
	Name       string `json:"name" jsonschema:"managed node name"`
	IntentPath string `json:"intent_path" jsonschema:"absolute path to the intent.yaml to render against"`
	Context    int    `json:"context,omitempty" jsonschema:"number of context lines around each diff (default 0)"`
}

func registerDriftTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "verify_config",
		Title:       "Compare live config to intent",
		Description: "Pull the .conf currently in use by the running node, render fresh HOCON from --intent, return diffs[]. Read-only. Equivalent to `trond verify-config <node> --intent <path> -o json`. Use this as a cheap reconcile signal before deciding whether apply is warranted.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, verifyConfigTool)
}

func verifyConfigTool(ctx context.Context, _ *mcp.CallToolRequest, args verifyConfigArgs) (*mcp.CallToolResult, any, error) {
	if args.Name == "" {
		return errResult(fmt.Errorf("name is required"))
	}
	if args.IntentPath == "" {
		return errResult(fmt.Errorf("intent_path is required"))
	}

	parsed, err := intent.Load(args.IntentPath)
	if err != nil {
		return errResult(err)
	}

	store, err := state.NewStore(paths.State())
	if err != nil {
		return errResult(err)
	}
	st, err := store.Load()
	if err != nil {
		return errResult(err)
	}
	node := store.GetNode(st, args.Name)
	if node == nil {
		return errResult(notFound("verify_config", args.Name))
	}

	tgt, err := mcpResolveTargetFromNode(node)
	if err != nil {
		return errResult(err)
	}
	if c, ok := any(tgt).(interface{ Close() error }); ok {
		defer c.Close()
	}

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	live, err := readLiveConfigForMCP(probeCtx, tgt, node)
	if err != nil {
		return errResult(err)
	}

	desired, err := render.RenderHOCON("", parsed, &parsed.Nodes[0])
	if err != nil {
		return errResult(err)
	}

	diffs := mcpLineDiff(live, desired, args.Context)
	return jsonResult(map[string]any{
		"name":          args.Name,
		"intent":        parsed.Name,
		"intent_path":   args.IntentPath,
		"in_sync":       len(diffs) == 0,
		"live_lines":    countMCPLines(live),
		"desired_lines": countMCPLines(desired),
		"diff_count":    len(diffs),
		"diffs":         diffs,
	})
}

// readLiveConfigForMCP duplicates cmd/verify_config.go's readLiveConfig
// to avoid the cmd → internal/mcp import edge. Not a public type so
// keeping it in the mcp package is fine.
func readLiveConfigForMCP(ctx context.Context, tgt target.Target, node *state.ManagedNode) (string, error) {
	if node.Runtime == "jar" {
		out, err := tgt.Exec(ctx, "cat", node.InstallPath+"/conf/"+node.Name+".conf")
		if err != nil {
			return "", fmt.Errorf("read jar conf: %w", err)
		}
		return string(out), nil
	}
	out, err := tgt.Exec(ctx, "docker", "exec", node.Name, "cat",
		"/java-tron/conf/"+node.Name+".conf")
	if err != nil {
		return "", fmt.Errorf("docker exec cat: %w", err)
	}
	return string(out), nil
}

// mcpLineDiff mirrors cmd.lineDiff. Same simplicity rationale.
func mcpLineDiff(live, desired string, ctxLines int) []string {
	a := strings.Split(strings.TrimRight(live, "\n"), "\n")
	b := strings.Split(strings.TrimRight(desired, "\n"), "\n")
	var diffs []string
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := range maxLen {
		var aLine, bLine string
		if i < len(a) {
			aLine = a[i]
		}
		if i < len(b) {
			bLine = b[i]
		}
		if aLine == bLine {
			continue
		}
		if ctxLines > 0 {
			lo := i - ctxLines
			if lo < 0 {
				lo = 0
			}
			for j := lo; j < i; j++ {
				if j < len(a) {
					diffs = append(diffs, "  "+a[j])
				}
			}
		}
		switch {
		case i < len(a) && i >= len(b):
			diffs = append(diffs, "- "+aLine)
		case i >= len(a) && i < len(b):
			diffs = append(diffs, "+ "+bLine)
		default:
			diffs = append(diffs, "- "+aLine)
			diffs = append(diffs, "+ "+bLine)
		}
	}
	return diffs
}

func countMCPLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
