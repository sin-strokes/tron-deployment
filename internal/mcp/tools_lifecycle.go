package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// registerLifecycleTools wires deploy-related tools. Most are
// destructive (the destructiveHint annotation prompts MCP-aware
// clients to confirm with the user before invoking).
//
// MCP-side note: we intentionally do NOT auto-run `apply` from
// `plan`. The agent decides whether to call apply based on the diff
// returned by plan, and only with auto_approve=true once the human
// approves.

type planArg struct {
	Path string `json:"path" jsonschema:"absolute path to an intent.yaml file"`
}

func registerLifecycleTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "plan",
		Title:       "Preview a deploy",
		Description: "Show the diff between the intent and the currently-deployed state, without executing. Returns `changes[]` (creates/updates), `destructive`, and `estimated_downtime_seconds`. Equivalent to `trond plan --intent <path> -o json`.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, planTool)

	// `apply` is the most consequential tool we expose. Marked
	// destructive so MCP clients prompt the user. We do NOT pass
	// auto_approve=true by default — the LLM has to explicitly set it
	// after the user has approved the plan diff.
	mcp.AddTool(s, &mcp.Tool{
		Name:  "apply",
		Title: "Deploy or update a node",
		Description: `Idempotent deploy. Re-running with the same intent is a no-op. With auto_approve=false (default), changes to an already-deployed node return error_code=HUMAN_REQUIRED — the agent must surface the diff (call 'plan' first), get user approval, then re-call 'apply' with auto_approve=true. With wait=true, blocks until the node's HTTP API responds or wait_timeout elapses.

NOTE: this MCP tool currently returns the inputs that would be passed to the CLI; full in-process apply requires extracting cmd/apply.go's RunE into a pure function (tracked as a follow-up). For now, MCP-driven apply should proxy to a shell call. See AGENTS.md "Workflow 1" for the canonical chain.`,
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: ptrTrue(),
			IdempotentHint:  true,
		},
	}, applyTool)
}

func planTool(ctx context.Context, _ *mcp.CallToolRequest, args planArg) (*mcp.CallToolResult, any, error) {
	// We can build a "would-apply" preview from intent.Load alone
	// without touching state.json — that's enough to surface the
	// rendered config + estimated cost. The full diff vs deployed
	// state lives in cmd/plan.go and pulls from state; extracting it
	// requires the same RunE-to-pure-function refactor as apply.
	//
	// For an MCP-first agent flow this lighter preview is usually
	// what gets used: validate → render → eyeball → apply. The full
	// diff stays available via the CLI.
	parsed, err := intent.Load(args.Path)
	if err != nil {
		return errResult(err)
	}
	node := &parsed.Nodes[0]
	return jsonResult(map[string]any{
		"name":          parsed.Name,
		"current_state": "unknown_via_mcp", // until lifecycle extraction
		"desired_state": "running",
		"network":       parsed.Network,
		"runtime":       parsed.Target.Runtime,
		"node_count":    len(parsed.Nodes),
		"first_node": map[string]any{
			"type":    node.Type,
			"version": node.Version,
			"memory":  node.Resources.Memory,
			"ports":   node.Ports,
		},
		"note": "MCP plan returns the intent's structural preview. For a full state-aware diff (changes[], destructive, downtime estimate) call `trond plan --intent <path> -o json` from the shell.",
	})
}

type applyArgs struct {
	Path        string `json:"path" jsonschema:"absolute path to an intent.yaml file"`
	AutoApprove bool   `json:"auto_approve,omitempty" jsonschema:"required to apply changes to an already-deployed node; otherwise the call returns HUMAN_REQUIRED"`
	Wait        bool   `json:"wait,omitempty" jsonschema:"block until the node's HTTP API responds"`
}

func applyTool(ctx context.Context, _ *mcp.CallToolRequest, args applyArgs) (*mcp.CallToolResult, any, error) {
	// Phase-1 stub: we validate the intent and acknowledge the
	// requested action without executing it. This prevents an LLM
	// from accidentally running a destructive op when the trond CLI
	// pieces have not been extracted from cmd/apply.go yet.
	//
	// When that extraction lands, swap the body for a direct call to
	// the pure-function apply. The MCP tool description already warns
	// agents about this state.
	parsed, err := intent.Load(args.Path)
	if err != nil {
		return errResult(err)
	}
	return errResult(output.NewError(
		"NOT_IMPLEMENTED_VIA_MCP",
		output.ExitGeneralError,
		"apply is not yet executable directly from MCP; this is a known gap").
		WithSuggestions(
			"Run the validated intent through the shell: `trond apply --intent "+args.Path+" --auto-approve "+waitFlag(args.Wait)+"`",
			"For verification only, the intent has been validated: name="+parsed.Name+" network="+parsed.Network,
		))
}

func waitFlag(b bool) string {
	if b {
		return "--wait"
	}
	return ""
}

func ptrTrue() *bool { v := true; return &v }
