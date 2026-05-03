package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestMCPToolDescriptions enforces a quality bar on every registered
// tool's Description and Title. The MCP client surfaces these to the
// LLM as the only signal for "when should I use this tool?". A
// missing or sloppy description silently degrades agent UX.
//
// Rules:
//   - Title is set (LLM sees this in the picker UI).
//   - Description is set, ≥ 30 characters.
//   - Description doesn't contain placeholder text (TODO / FIXME / XXX
//     / "lorem ipsum") — easy to forget at PR time.
//   - Read-side tools mention "Equivalent to `trond <cli>`" so an
//     operator reading MCP traces can map back to the CLI command.
//     Documented exceptions live in skipCLIHint with a one-line reason.
func TestMCPToolDescriptions(t *testing.T) {
	skipCLIHint := map[string]string{
		// Tools whose MCP variant has no 1:1 CLI equivalent.
		"plan":    "MCP plan returns intent-only preview; CLI plan is state-aware (different shape).",
		"inspect": "MCP variant always returns ALL nodes; CLI inspect is selector-driven.",
	}

	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	for _, tool := range res.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Title == "" {
				t.Errorf("Title empty — clients show this in their tool picker")
			}
			if len(tool.Description) < 30 {
				t.Errorf("Description too short (%d chars). LLMs need at least a sentence to know when to call this tool. Got: %q",
					len(tool.Description), tool.Description)
			}
			lowered := strings.ToLower(tool.Description)
			for _, placeholder := range []string{"todo", "fixme", "xxx", "lorem ipsum"} {
				if strings.Contains(lowered, placeholder) {
					t.Errorf("Description contains placeholder %q — finalise before merging.\nfull text: %q",
						placeholder, tool.Description)
				}
			}
			if _, skip := skipCLIHint[tool.Name]; skip {
				return
			}
			// Heuristic: read-side tool descriptions are written to
			// say "Equivalent to `trond ...`" so an operator can
			// trace MCP calls back to the CLI command they would
			// invoke directly. Allowed wording variants:
			//   - "Equivalent to `trond ..."
			//   - "Equivalent to \"trond ..."
			//   - "= `trond ..."
			//   - "`trond ..." anywhere (lenient form)
			if !strings.Contains(tool.Description, "trond ") {
				t.Errorf("Description should reference the CLI command (`trond <cmd>`) so MCP traces are mappable.\ngot: %q", tool.Description)
			}
		})
	}
}
