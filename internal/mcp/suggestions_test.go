package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPSuggestionsAreActionable verifies the suggestions[] array on
// MCP error envelopes is non-empty and the entries actually look
// actionable — they should reference a command, path, or env var the
// agent can plausibly try. AGENTS.md tells agents to attempt
// `suggestions[0]` first; if that's empty or vague, the agent loop
// stalls.
//
// We trigger known error envelopes via tool calls and inspect the
// suggestions[] of each.
func TestMCPSuggestionsAreActionable(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args json.RawMessage
		// hintMustContain checks at least ONE suggestion contains
		// any of these substrings. Loose by design — we want to
		// catch "no suggestion at all" and "lorem ipsum" but not
		// over-fit to specific wording.
		hintMustContain []string
	}{
		{
			name: "missing-intent-path",
			tool: "config_validate",
			args: json.RawMessage(`{"path":"/no/such/intent.yaml"}`),
			// config_validate's MCP error envelope wraps the
			// internal/intent.Load error verbatim — accept anything
			// that points at the intent file in any wording.
			hintMustContain: []string{"intent", "config", "examples", "yaml"},
		},
		{
			name: "missing-node",
			tool: "status",
			args: json.RawMessage(`{"name":"definitely-not-deployed"}`),
			// MCP suggestions reference MCP tool names (list, apply)
			// because that's what the calling agent would invoke
			// next, not the CLI binary.
			hintMustContain: []string{"'list'", "'apply'"},
		},
		{
			name:            "missing-knowledge-topic",
			tool:            "knowledge_get",
			args:            json.RawMessage(`{"topic":"definitely-not-a-topic"}`),
			hintMustContain: []string{"knowledge_list"},
		},
	}

	session, cleanup := newConnectedPair(t)
	defer cleanup()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected IsError=true; success body: %s", extractText(t, res))
			}
			body := extractText(t, res)
			var envelope map[string]any
			if err := json.Unmarshal([]byte(body), &envelope); err != nil {
				t.Fatalf("envelope not JSON: %v\n%s", err, body)
			}
			suggestions, _ := envelope["suggestions"].([]any)
			if len(suggestions) == 0 {
				t.Fatalf("error envelope has no suggestions[] (agents need at least 1 actionable hint)\nbody: %s",
					body)
			}
			// First suggestion should be something the LLM can try
			// — at minimum non-empty and mention a verb / command.
			first, _ := suggestions[0].(string)
			if len(first) < 10 {
				t.Errorf("suggestions[0] too short to be actionable (%q)", first)
			}
			// Heuristic: at least one suggestion should mention any
			// of the expected hints. Catches generic "Try again"
			// type non-suggestions.
			joined := ""
			for _, s := range suggestions {
				if str, ok := s.(string); ok {
					joined += str + "\n"
				}
			}
			matched := false
			for _, h := range tc.hintMustContain {
				if strings.Contains(joined, h) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("suggestions[] should mention at least one of %v\nactual: %v",
					tc.hintMustContain, suggestions)
			}
		})
	}
}
