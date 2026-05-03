package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestPrompts_ListAndGet exercises every registered prompt: list
// surfaces them all, each renders with required arguments, missing
// arguments produce a clean error.
func TestPrompts_ListAndGet(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	got := map[string]*mcpsdk.Prompt{}
	for _, p := range res.Prompts {
		got[p.Name] = p
	}
	wantNames := []string{
		"deploy_fullnode",
		"diagnose_failing_node",
		"setup_private_network",
		"recover_failed_upgrade",
	}
	for _, n := range wantNames {
		if _, ok := got[n]; !ok {
			t.Errorf("prompt %q missing from ListPrompts", n)
		}
	}

	cases := []struct {
		name   string
		args   map[string]string
		expect string // substring that must appear in the rendered prompt
	}{
		{
			name:   "deploy_fullnode",
			args:   map[string]string{"intent_path": "/tmp/intent.yaml"},
			expect: "config_validate (path=\"/tmp/intent.yaml\"",
		},
		{
			name:   "diagnose_failing_node",
			args:   map[string]string{"node": "my-fullnode"},
			expect: "diagnose (name=\"my-fullnode\")",
		},
		{
			name:   "setup_private_network",
			args:   map[string]string{"intent_path": "/tmp/private.yaml"},
			expect: "trond network create --intent /tmp/private.yaml",
		},
		{
			name:   "recover_failed_upgrade",
			args:   map[string]string{"node": "my-fullnode"},
			expect: "trond rollback my-fullnode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := session.GetPrompt(context.Background(),
				&mcpsdk.GetPromptParams{Name: tc.name, Arguments: tc.args})
			if err != nil {
				t.Fatalf("GetPrompt: %v", err)
			}
			if len(out.Messages) == 0 {
				t.Fatalf("prompt %s returned no messages", tc.name)
			}
			text := promptText(out.Messages[0])
			if !strings.Contains(text, tc.expect) {
				t.Errorf("prompt %s: expected substring %q\nactual:\n%s",
					tc.name, tc.expect, text)
			}
		})
	}
}

// TestPrompts_RejectMissingArgument confirms that omitting a
// required argument fails the GetPrompt call rather than rendering
// a half-templated string. Without this guard, the prompt would
// surface a literal "%!s(MISSING)" or empty path to the LLM.
func TestPrompts_RejectMissingArgument(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	_, err := session.GetPrompt(context.Background(),
		&mcpsdk.GetPromptParams{Name: "deploy_fullnode"})
	if err == nil {
		t.Error("expected error for missing intent_path argument")
	}
}

// promptText extracts the text content from a PromptMessage. The
// Content field is an interface; we type-assert to TextContent
// since every prompt in this package emits text only.
func promptText(m *mcpsdk.PromptMessage) string {
	if tc, ok := m.Content.(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}
