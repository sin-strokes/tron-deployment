package mcp

import (
	"encoding/json"

	"github.com/tronprotocol/tron-deployment/internal/schema"
)

// schemaManifestJSON returns a compact manifest exposed via the
// `trond://schema-manifest` resource. We avoid pulling the full
// cobra-tree walker (lives in cmd/schema.go) so this stays
// importable from internal/mcp without a cycle. Callers that want
// the full command/flag manifest should shell out to
// `trond schema -o json`.
//
// Shape:
//
//	{
//	  "schema_version": "1.x.0",
//	  "schemas": {
//	    "apply":  { ...full schema object... },
//	    ...
//	  }
//	}
func schemaManifestJSON() (string, error) {
	out := struct {
		SchemaVersion string                    `json:"schema_version"`
		Schemas       map[string]map[string]any `json:"schemas"`
	}{
		SchemaVersion: schema.SchemaVersion,
		Schemas:       map[string]map[string]any{},
	}
	for _, name := range schema.Names() {
		if doc, ok := schema.Get(name); ok {
			out.Schemas[name] = doc
		}
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}
