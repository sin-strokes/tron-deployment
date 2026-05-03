package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

// registerResources wires read-only data sources as MCP resources.
// Resources are conceptually different from tools: tools are
// "actions" (verb), resources are "data" (noun). MCP-aware clients
// like Claude Desktop surface resources in a file-system-like UI
// the user can attach to a conversation, while tools are invoked by
// the LLM during reasoning.
//
// What we expose:
//
//   - trond://state         — current state.json (every managed node + targets)
//   - trond://audit-log     — last 200 lines of audit.log (JSONL)
//   - trond://schema-manifest — entire schema manifest the schema command emits
//
// Why these three: they're read often, change rarely within a
// conversation, and have stable URIs that survive multiple tool
// calls (so an LLM can re-attach the same resource without an
// extra round-trip).
//
// Resources that *would* be useful but aren't here yet:
//   - trond://nodes/<name>/endpoints — needs ResourceTemplate (URI template)
//   - trond://intent/<name>          — same
//
// The dynamic per-node URIs are TODO; the static ones are MVP.
func registerResources(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		URI:         "trond://state",
		Name:        "state.json",
		Description: "The state.json file at $TROND_STATE_DIR (or ~/.trond). One row per managed node — name, version, target, runtime, ports. The agent can attach this to a conversation to reason about the deployed estate without per-node tool calls.",
		MIMEType:    "application/json",
	}, readStateResource)

	s.AddResource(&mcp.Resource{
		URI:         "trond://audit-log",
		Name:        "audit.log (recent)",
		Description: "Up to the last 200 entries of audit.log (JSONL). Use this to answer questions like 'what did we do to this node yesterday' without invoking the events tool.",
		MIMEType:    "application/x-ndjson",
	}, readAuditLogResource)

	s.AddResource(&mcp.Resource{
		URI:         "trond://schema-manifest",
		Name:        "schema manifest",
		Description: "The full output of `trond schema -o json` — schema_version, every command, every flag, every output schema. Use this resource as the agent's authoritative reference for the trond CLI surface.",
		MIMEType:    "application/json",
	}, readSchemaManifestResource)
}

func readStateResource(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	store, err := state.NewStore(paths.State())
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	st, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(body),
		}},
	}, nil
}

const auditLogTailMax = 200

func readAuditLogResource(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	body, err := os.ReadFile(paths.AuditLog())
	if err != nil {
		if os.IsNotExist(err) {
			// No log yet — empty resource is more useful than an
			// error; agents reason fine over zero entries.
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      req.Params.URI,
					MIMEType: "application/x-ndjson",
					Text:     "",
				}},
			}, nil
		}
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "application/x-ndjson",
			Text:     tailLines(string(body), auditLogTailMax),
		}},
	}, nil
}

func readSchemaManifestResource(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	// We can't import internal/schema's manifest builder here without
	// a circular dependency (cmd/schema.go owns the cobra-tree walk).
	// Instead we point at the embedded SchemaVersion + the full set
	// of output schemas — the union of which is the manifest's
	// load-bearing content. Agents that want the full manifest can
	// shell out to `trond schema -o json`.
	body, err := schemaManifestJSON()
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     body,
		}},
	}, nil
}

// tailLines returns at most n lines from the end of s, newline-
// preserved. Used to bound the audit-log resource size — a
// long-running operator's log can be megabytes.
func tailLines(s string, n int) string {
	if s == "" {
		return ""
	}
	// Walk backward counting newlines; take everything from the
	// (n+1)-th newline from the end onwards.
	count := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			count++
			if count > n {
				return s[i+1:]
			}
		}
	}
	return s
}
