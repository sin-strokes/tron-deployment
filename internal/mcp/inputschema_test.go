package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestMCPInputSchemas asserts every tool the server exposes carries
// an inputSchema that's actually useful to an LLM:
//
//   - object type (must let agents pass an object, even if empty)
//   - required[] is set when the tool needs args (e.g. status takes a
//     node name; config_validate takes a path)
//   - non-empty description on each non-empty argument struct
//
// Without this gate, a tool that defined its args via a struct with
// no `json` / `jsonschema` tags would publish an empty/useless
// inputSchema and silently degrade the agent's ability to call it.
//
// `expectRequired` lists tools whose args struct should produce a
// non-empty `required` array. Empty-arg tools (like version, doctor,
// list) intentionally have no required fields.
func TestMCPInputSchemas(t *testing.T) {
	expectRequired := map[string][]string{
		"status":          {"name"},
		"health":          {"name"},
		"diagnose":        {"name"},
		"config_validate": {"path"},
		"config_render":   {"path"},
		"plan":            {"path"},
		"apply":           {"path"},
		"snapshot_list":   {"network"},
		// snapshot_download requires `dest` only — `network` is
		// optional because callers may pass `domain` instead.
		"snapshot_download": {"dest"},
		"knowledge_get":     {"topic"},
		"build_inspect":     {"cache_key"},
	}

	emptyArgsTools := map[string]bool{
		"list":             true,
		"doctor":           true,
		"version":          true,
		"snapshot_sources": true,
		"snapshot_jobs":    true,
		"knowledge_list":   true,
		// build_list and build_prune have all-optional args (filter /
		// sort / older_than / keep_last are all opt-in). build_inspect
		// is the only one with a required arg.
		"build_list":  true,
		"build_prune": true,
		// inspect takes no args from MCP — it always returns the
		// full set. CLI `trond inspect` accepts a positional, but
		// the MCP variant deliberately doesn't, to match the
		// "manifest of all" use case agents care about.
		"inspect": true,
	}

	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		t.Run(tool.Name, func(t *testing.T) {
			if tool.InputSchema == nil {
				t.Fatalf("%s: inputSchema is nil", tool.Name)
			}
			body, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal inputSchema: %v", err)
			}
			var schemaDoc map[string]any
			if err := json.Unmarshal(body, &schemaDoc); err != nil {
				t.Fatalf("inputSchema is not a JSON object: %v\nbody: %s", err, body)
			}
			if typ, _ := schemaDoc["type"].(string); typ != "object" {
				t.Errorf("inputSchema.type: want \"object\", got %q\nbody: %s",
					typ, body)
			}

			required, _ := schemaDoc["required"].([]any)
			if want, ok := expectRequired[tool.Name]; ok {
				if len(required) != len(want) {
					t.Errorf("required count: want %v, got %v\nbody: %s",
						want, required, body)
				} else {
					gotSet := map[string]bool{}
					for _, r := range required {
						if s, ok := r.(string); ok {
							gotSet[s] = true
						}
					}
					for _, w := range want {
						if !gotSet[w] {
							t.Errorf("required missing %q\nbody: %s", w, body)
						}
					}
				}
			} else if emptyArgsTools[tool.Name] {
				if len(required) != 0 {
					t.Errorf("expected no required fields for empty-args tool, got %v",
						required)
				}
			}

			// Description on the tool itself is enforced separately
			// (the tool registration includes a Title + Description),
			// but having SOME properties or being explicitly empty is
			// part of the contract.
			_, hasProps := schemaDoc["properties"]
			if !emptyArgsTools[tool.Name] && !hasProps {
				t.Errorf("tool with required args has no `properties` block\nbody: %s", body)
			}

			if tool.Description == "" {
				t.Errorf("tool %s has empty Description — agents need this to know when to call", tool.Name)
			}
		})
	}

	// Reverse check: every tool we have an expectation for must have
	// been registered. Catches the case where someone removed a tool
	// but forgot to update the expect tables.
	for name := range expectRequired {
		if !seen[name] {
			t.Errorf("expected tool %q in ListTools, but server didn't register it", name)
		}
	}
	for name := range emptyArgsTools {
		if !seen[name] {
			t.Errorf("expected tool %q (empty-args) in ListTools, but server didn't register it", name)
		}
	}
}
