package schema

import (
	"encoding/json"
	"strings"
	"testing"

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
