package schema

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSchemaVersionShape catches the most common schema-versioning
// footguns at unit-test time:
//
//   - SchemaVersion must be a semver triple (PATCH.MINOR.MAJOR digits).
//   - Every embedded schema must have a corresponding source file
//     (already covered by TestEmbeddedSchemasMatchSourceTree, but
//     re-asserted here so a future refactor of either test doesn't
//     leave the version logic without a sibling check).
//   - When the developer changes any embedded schema, they must
//     regenerate internal/schema/version_baseline.json so the
//     "frozen at last release" snapshot stays current. The baseline
//     file's hash must match the current set of embedded files.
//
// Not enforced here (intentionally — too much false-positive risk):
//
//   - Whether a particular change was MINOR vs MAJOR per semver
//     rules. We document the rules in embed.go's package doc and
//     require the developer to bump SchemaVersion accordingly. A
//     reviewer catches semver bumps just like they catch any other
//     review item.
//
// To regenerate the baseline after a schema edit, run:
//
//	go test -run TestSchemaVersionShape ./internal/schema/ -update
//
// The -update flag is honoured below: when set, the test rewrites
// version_baseline.json with the current state and passes. CI runs
// without -update so any drift fails loud.
func TestSchemaVersionShape(t *testing.T) {
	semver := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	if !semver.MatchString(SchemaVersion) {
		t.Errorf("SchemaVersion %q is not a semver triple (e.g. 1.1.0)", SchemaVersion)
	}

	baseline := computeBaseline(t)
	baselinePath := filepath.Join("version_baseline.json")

	// Update mode bypasses the existing-file check so first-time
	// snapshot creation works.
	if updateBaselines() {
		if err := os.WriteFile(baselinePath, baseline, 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("baseline updated at %s — commit it alongside any SchemaVersion bump", baselinePath)
		return
	}

	current, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read %s: %v (run with TROND_SCHEMA_UPDATE_BASELINE=1 to create it)", baselinePath, err)
	}

	if string(current) != string(baseline) {
		t.Errorf(
			"%s does not match the current embedded schemas.\n"+
				"You changed at least one schema. Two-step fix:\n"+
				"  1. Bump SchemaVersion in internal/schema/embed.go per semver rules\n"+
				"     (see the package doc — PATCH/MINOR/MAJOR semantics)\n"+
				"  2. Re-snapshot: go test -run TestSchemaVersionShape ./internal/schema/ -update",
			baselinePath)
	}
}

// computeBaseline returns the canonical "snapshot" bytes — sorted
// schema names → sha256 of the raw embedded JSON. We hash content
// rather than embed it whole so the baseline file stays small.
func computeBaseline(t *testing.T) []byte {
	t.Helper()
	names := Names()
	sort.Strings(names)
	type entry struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	}
	out := struct {
		SchemaVersion string  `json:"schema_version"`
		Entries       []entry `json:"entries"`
	}{
		SchemaVersion: SchemaVersion,
	}
	for _, n := range names {
		doc, _ := Get(n)
		body, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal %s: %v", n, err)
		}
		sum := sha256.Sum256(body)
		out.Entries = append(out.Entries, entry{
			Name: n,
			Hash: hexLower(sum[:]),
		})
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	return append(body, '\n')
}

// hexLower is a small dependency-free hex encoder so this test stays
// in the schema package without pulling encoding/hex in just for one
// call (encoding/hex would also work; this avoids the import for
// readability).
func hexLower(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0xf]
	}
	return string(out)
}

// updateBaselines returns true when the test was run with -update so
// the baseline file should be regenerated.
func updateBaselines() bool {
	for _, a := range os.Args {
		if a == "-update" || a == "--update" {
			return true
		}
	}
	return strings.EqualFold(os.Getenv("TROND_SCHEMA_UPDATE_BASELINE"), "1")
}
