//go:build e2e

package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestE2E_LocalDockerFullnode tests the full lifecycle:
// apply → status → stop → start → remove
// Requires Docker to be running.
func TestE2E_LocalDockerFullnode(t *testing.T) {
	// Check Docker is available
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available, skipping e2e test")
	}

	binary := buildBinary(t)
	intentPath := absPath(t, "../examples/mainnet-fullnode.yaml")

	// 1. Validate
	out := runTrond(t, binary, "config", "validate", intentPath, "--output", "json")
	var validateResult map[string]any
	mustUnmarshal(t, out, &validateResult)
	if validateResult["valid"] != true {
		t.Fatalf("validate failed: %s", out)
	}
	t.Log("validate: OK")

	// 2. Apply
	out = runTrond(t, binary, "apply", "--intent", intentPath, "--output", "json", "--auto-approve")
	var applyResult map[string]any
	mustUnmarshal(t, out, &applyResult)
	if applyResult["status"] != "running" {
		t.Fatalf("apply status not running: %s", out)
	}
	t.Log("apply: OK")

	// 3. Status
	out = runTrond(t, binary, "status", "my-fullnode", "--output", "json")
	var statusResult map[string]any
	mustUnmarshal(t, out, &statusResult)
	if statusResult["status"] != "running" {
		t.Fatalf("status not running: %s", out)
	}
	t.Log("status: OK")

	// 4. List
	out = runTrond(t, binary, "list", "--output", "json")
	var listResult []map[string]any
	mustUnmarshal(t, out, &listResult)
	if len(listResult) == 0 {
		t.Fatal("list returned empty")
	}
	t.Log("list: OK")

	// 5. Stop
	out = runTrond(t, binary, "stop", "my-fullnode", "--output", "json")
	var stopResult map[string]any
	mustUnmarshal(t, out, &stopResult)
	if stopResult["status"] != "stopped" {
		t.Fatalf("stop status not stopped: %s", out)
	}
	t.Log("stop: OK")

	// 6. Start
	out = runTrond(t, binary, "start", "my-fullnode", "--output", "json")
	var startResult map[string]any
	mustUnmarshal(t, out, &startResult)
	if startResult["status"] != "running" {
		t.Fatalf("start status not running: %s", out)
	}
	t.Log("start: OK")

	// 7. Remove
	out = runTrond(t, binary, "remove", "my-fullnode", "--confirm", "my-fullnode", "--output", "json")
	var removeResult map[string]any
	mustUnmarshal(t, out, &removeResult)
	if removeResult["status"] != "removed" {
		t.Fatalf("remove status not removed: %s", out)
	}
	t.Log("remove: OK")

	// 8. Verify list is empty
	out = runTrond(t, binary, "list", "--output", "json")
	var finalList []map[string]any
	mustUnmarshal(t, out, &finalList)
	if len(finalList) != 0 {
		t.Fatalf("list not empty after remove: %s", out)
	}
	t.Log("full lifecycle: PASS")
}

func buildBinary(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	binary := filepath.Join(tmpDir, "trond")
	cmd := exec.Command("go", "build", "-o", binary, "..")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	return binary
}

func runTrond(t *testing.T, binary string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Join("..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("trond %v failed: %v\noutput: %s", args, err, out)
	}
	return out
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal failed: %v\ndata: %s", err, data)
	}
}

func absPath(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return abs
}
