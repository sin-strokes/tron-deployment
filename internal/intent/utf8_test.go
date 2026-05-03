package intent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_NonASCIIDescription verifies trond accepts UTF-8 in
// description / labels — which it should, since YAML and Go's
// encoding/json both default to UTF-8. We also check that the
// values round-trip without re-encoding mangling.
//
// Why test it: a future regression that ran content through a
// non-UTF-8-safe codec (e.g. a string-replacement step that
// assumes ASCII) would only be caught here. Reasonable agents
// pass labels like role=主节点 or descriptions with emoji as
// readable hints.
func TestLoad_NonASCIIDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "intent.yaml")
	body := `name: test-utf8
target:
  type: local
  runtime: docker
network: nile
nodes:
  - type: fullnode
    version: latest
    labels:
      team: 区块链团队
      emoji: 🌏-asia
    resources:
      memory: 4GB
    ports:
      http: 8090
      grpc: 50051
      p2p: 18888
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	parsed, err := Load(path)
	if err != nil {
		t.Fatalf("Load with UTF-8 labels: %v", err)
	}
	if len(parsed.Nodes) == 0 {
		t.Fatal("intent has no nodes")
	}
	labels := parsed.Nodes[0].Labels
	if got := labels["team"]; got != "区块链团队" {
		t.Errorf("UTF-8 label mangled: got %q, want %q", got, "区块链团队")
	}
	if got := labels["emoji"]; got != "🌏-asia" {
		t.Errorf("emoji label mangled: got %q, want %q", got, "🌏-asia")
	}
}

// TestLoad_RejectsNULBytes guards against a path injection / null-
// byte trick where an attacker (or sloppy automation) embeds a NUL
// in a node name to bypass downstream string handling. trond's
// safe_string validator should refuse it.
func TestLoad_RejectsNULBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "intent.yaml")
	body := "name: \"test\\u0000nul\"\n" +
		"target: {type: local, runtime: docker}\nnetwork: nile\n" +
		"nodes:\n  - type: fullnode\n    version: latest\n" +
		"    resources: {memory: 4GB}\n" +
		"    ports: {http: 8090, grpc: 50051, p2p: 18888}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected Load to reject NUL byte in name; got success")
	}
}
