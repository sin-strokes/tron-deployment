package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"
)

func TestEmbeddedSchemasParseAndDeclareID(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			doc, ok := Get(name)
			if !ok {
				t.Fatalf("Get(%q) returned !ok", name)
			}
			// Round-trip: must marshal back to valid JSON. Catches any
			// embed-time mutation introduced by a future cloneMap bug.
			if _, err := json.Marshal(doc); err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			// $id must reference the canonical URL — keeps the file
			// system layout and the public contract aligned.
			id, _ := doc["$id"].(string)
			if !strings.HasPrefix(id, JSONSchemaURLBase) {
				t.Fatalf("$id %q does not start with %q", id, JSONSchemaURLBase)
			}
			// $schema must point at draft 2020-12 — bumping the draft
			// without coordinating with the embedded $defs would silently
			// break clients.
			ref, _ := doc["$schema"].(string)
			if !strings.Contains(ref, "draft/2020-12") {
				t.Fatalf("$schema %q is not draft 2020-12", ref)
			}
		})
	}
}

func TestGetClonesPerCall(t *testing.T) {
	// Caller mutations must not leak back into rawSchemas; otherwise
	// two callers in the same process would see each other's writes.
	a, _ := Get("apply")
	a["title"] = "MUTATED"
	b, _ := Get("apply")
	if b["title"] == "MUTATED" {
		t.Fatal("Get returned a shared map; clone is not happening")
	}
}

// TestEmbeddedSchemasCompileAsJSONSchema is the structural validity
// gate. The earlier TestEmbeddedSchemasParseAndDeclareID only checks
// that the file is well-formed JSON and has a few top-level keys; a
// schema can pass that and still be a malformed JSON Schema (e.g.
// `"required": "name"` instead of `["name"]`, `$ref` to a missing
// `$defs` entry, or a draft-incompatible keyword shape).
//
// Compiling each schema with santhosh-tekuri/jsonschema is the cheapest
// way to catch every such bug at unit-test time. Failures print a
// path inside the schema so the bad keyword is easy to find.
func TestEmbeddedSchemasCompileAsJSONSchema(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			doc, _ := Get(name)
			c := jsonschema.NewCompiler()
			// Use the schema's own $id as the resource URL when present;
			// fall back to a synthetic URL so the compiler can resolve
			// internal $refs even for older schemas missing $id.
			id, _ := doc["$id"].(string)
			if id == "" {
				id = "trond:" + name
			}
			if err := c.AddResource(id, doc); err != nil {
				t.Fatalf("AddResource: %v", err)
			}
			if _, err := c.Compile(id); err != nil {
				t.Fatalf("schema %q is not a valid JSON Schema: %v", name, err)
			}
		})
	}
}

// TestEmbeddedSchemasMatchSourceTree guards against drift between the
// committed schemas under schemas/ and the copies bundled into the
// binary at internal/schema/files/. Both must stay in sync because:
//
//   - schemas/ is what GitHub renders and what the published $id
//     URLs resolve to (agents may fetch them online).
//   - internal/schema/files/ is what `trond schema -o json` exposes
//     at runtime (offline agents read these).
//
// A drift between the two means online and offline agents see
// different contracts for the same trond version.
//
// The test is content-equality, not byte-equality: JSON whitespace
// differences (one trailing newline, one not) do not count. Anything
// that matters semantically does count.
func TestEmbeddedSchemasMatchSourceTree(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			srcPath := filepath.Join(repoRoot, "schemas", "output", name+".schema.json")
			// "error" is the only schema that lives at schemas/output/;
			// all others follow the same naming. Some special-case
			// schemas could live at schemas/ root but currently none
			// do — fall back to that path if the output/ copy is
			// missing, so the test stays robust to future moves.
			data, err := os.ReadFile(srcPath)
			if err != nil {
				altPath := filepath.Join(repoRoot, "schemas", name+".schema.json")
				data, err = os.ReadFile(altPath)
				if err != nil {
					t.Fatalf("source schema for %q not found at %s or %s",
						name, srcPath, altPath)
				}
			}
			var srcDoc map[string]any
			if err := json.Unmarshal(data, &srcDoc); err != nil {
				t.Fatalf("source %s is not valid JSON: %v", srcPath, err)
			}
			embedded, _ := Get(name)
			if !mapsEqualJSON(srcDoc, embedded) {
				t.Fatalf("schema %q drift between source tree and embedded copy.\n"+
					"Re-sync via: cp schemas/output/%s.schema.json internal/schema/files/",
					name, name)
			}
		})
	}
}

// mapsEqualJSON compares two maps by re-encoding through encoding/json.
// Cheaper than rolling a deep-equal that handles all map[string]any
// shapes correctly (json.Number vs float64, ordering of object keys
// during marshal — which is sorted by encoding/json).
func mapsEqualJSON(a, b map[string]any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}

func TestBuild_ProducesExpectedSurface(t *testing.T) {
	root := &cobra.Command{Use: "trond"}
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Deploy",
		Run:   func(*cobra.Command, []string) {},
	}
	apply.Flags().String("intent", "", "Path to intent.yaml (required)")
	if err := apply.MarkFlagRequired("intent"); err != nil {
		t.Fatal(err)
	}
	root.AddCommand(apply)

	m := Build(root, map[string]string{"trond apply": "apply"})

	if m.SchemaVersion == "" {
		t.Fatal("expected non-empty SchemaVersion")
	}
	if len(m.Commands) != 1 || m.Commands[0].Name != "apply" {
		t.Fatalf("expected one apply command, got %+v", m.Commands)
	}
	cmd := m.Commands[0]
	if cmd.OutputSchema == nil {
		t.Fatal("expected output schema to be attached for apply")
	}
	if cmd.OutputSchemaURL == "" {
		t.Fatal("expected output_schema_url to be set")
	}
	// Required-flag detection — verify the cobra annotation probe still
	// matches the real cobra constant. If cobra renames the annotation
	// in a future release this test catches it.
	var foundIntent bool
	for _, f := range cmd.Flags {
		if f.Name == "intent" {
			foundIntent = true
			if !f.Required {
				t.Fatalf("intent flag should be Required, got %+v", f)
			}
		}
	}
	if !foundIntent {
		t.Fatal("intent flag missing from manifest")
	}
}

func TestBuild_SkipsHiddenAndHelp(t *testing.T) {
	root := &cobra.Command{Use: "trond"}
	root.AddCommand(&cobra.Command{Use: "real", Short: "...", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "hidden", Hidden: true, Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "help", Run: func(*cobra.Command, []string) {}})

	m := Build(root, nil)
	if len(m.Commands) != 1 || m.Commands[0].Name != "real" {
		t.Fatalf("expected only 'real' to survive filter, got %+v", m.Commands)
	}
}

func TestURLFor(t *testing.T) {
	got := URLFor("apply")
	if !strings.HasSuffix(got, "/apply.schema.json") {
		t.Fatalf("URL %q does not end with /apply.schema.json", got)
	}
}
