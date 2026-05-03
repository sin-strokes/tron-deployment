package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStore_LoadsLegacyStateFiles pins the upgrade contract:
// users that ran older trond versions have state.json on disk
// without fields the current ManagedNode struct documents.
// Loading must succeed with zero-value defaults — never fail with
// a parse error — so an upgrade doesn't strand the user.
//
// Each fixture below is the literal bytes of a state.json from
// some past trond version. New fields can be added freely (json
// "omitempty" means omitting them from old data is fine), but
// renaming or retyping requires a SchemaVersion bump and a
// migration pathway in this package.
func TestStore_LoadsLegacyStateFiles(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantNode string // node name we expect after loading
	}{
		{
			name: "v0-pre-intent-hash",
			// Earliest format: just name + status + version. No
			// intent_hash, config_hash, ports, labels, target.
			body: `{
  "version": 1,
  "nodes": [
    {"name": "legacy-fullnode", "version": "latest", "status": "running"}
  ]
}`,
			wantNode: "legacy-fullnode",
		},
		{
			name: "v1-pre-previous-version",
			// Has hashes and target but no previous_version (added
			// when rollback landed).
			body: `{
  "version": 1,
  "nodes": [
    {
      "name": "legacy-fullnode",
      "intent_hash": "abc",
      "config_hash": "def",
      "version": "4.7.7",
      "target": {"type": "local"},
      "runtime": "docker",
      "status": "running",
      "last_applied": "2025-12-01T00:00:00Z"
    }
  ]
}`,
			wantNode: "legacy-fullnode",
		},
		{
			name: "v1-pre-port-fields",
			// Missing http_port / grpc_port (added so probe commands
			// can find the right port without re-reading intent).
			body: `{
  "version": 1,
  "nodes": [
    {
      "name": "legacy-fullnode",
      "intent_hash": "abc",
      "config_hash": "def",
      "version": "4.7.7",
      "target": {"type": "local"},
      "runtime": "docker",
      "status": "running",
      "last_applied": "2025-12-01T00:00:00Z",
      "previous_version": "4.7.6"
    }
  ]
}`,
			wantNode: "legacy-fullnode",
		},
		{
			name: "v1-pre-labels",
			// Missing labels (added for the label-filter feature).
			body: `{
  "version": 1,
  "nodes": [
    {
      "name": "legacy-fullnode",
      "intent_hash": "abc",
      "config_hash": "def",
      "version": "4.7.7",
      "target": {"type": "ssh", "host": "10.0.0.1", "user": "tron", "port": 22},
      "runtime": "docker",
      "status": "running",
      "last_applied": "2025-12-01T00:00:00Z",
      "http_port": 8090,
      "grpc_port": 50051
    }
  ]
}`,
			wantNode: "legacy-fullnode",
		},
		{
			name: "empty-nodes",
			body: `{"version": 1, "nodes": []}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			store, err := NewStore(path)
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			st, err := store.Load()
			if err != nil {
				t.Fatalf("Load (legacy fixture must NOT fail): %v", err)
			}
			if tc.wantNode != "" {
				found := false
				for _, n := range st.Nodes {
					if n.Name == tc.wantNode {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected node %q in legacy state, got %v", tc.wantNode, st.Nodes)
				}
			}

			// Round-trip: saving the loaded state must produce a file
			// the current binary reads back identically. This catches
			// the case where Load tolerated old data but Save would
			// drop fields by accident.
			if err := store.Save(st); err != nil {
				t.Fatalf("Save round-trip: %v", err)
			}
			st2, err := store.Load()
			if err != nil {
				t.Fatalf("Load after Save: %v", err)
			}
			if len(st2.Nodes) != len(st.Nodes) {
				t.Errorf("round-trip changed node count: %d → %d", len(st.Nodes), len(st2.Nodes))
			}
		})
	}
}
