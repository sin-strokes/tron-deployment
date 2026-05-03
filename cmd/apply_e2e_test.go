//go:build e2e

package cmd

import (
	"context"
	"testing"
	"time"
)

// TestE2E_LocalDockerFullnode walks the full single-node lifecycle
// against a real local Docker daemon:
//
//	validate → apply → status → list → stop → start → remove → list-empty
//
// Skipped when Docker isn't reachable. The test owns its own state
// dir (TROND_STATE_DIR) so it can't collide with the developer's
// ~/.trond, and uses examples/mainnet-fullnode.yaml's `my-fullnode`
// node name so the apply / status / remove names line up with the
// intent file.
func TestE2E_LocalDockerFullnode(t *testing.T) {
	skipUnlessDocker(t)

	_, env := e2eEnv(t)
	intentPath := absExample(t, "examples/mainnet-fullnode.yaml")

	// Give the whole lifecycle a generous budget — pulling the
	// java-tron image from a cold cache can take a few minutes on
	// some networks.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// 1. Validate
	out := runTrondCtx(ctx, t, env, "config", "validate", intentPath, "--output", "json")
	var validateResult map[string]any
	mustUnmarshalJSON(t, out, &validateResult)
	if validateResult["valid"] != true {
		t.Fatalf("validate failed: %s", out)
	}
	t.Log("validate: OK")

	// 2. Apply (no --wait — the readiness probe takes minutes; container
	//    started is enough to drive the rest of the lifecycle).
	//
	// Apply's output schema (schemas/output/apply.schema.json) carries
	// `result` ∈ {created, updated, no_change} — there's no status
	// field. A successful first apply on an empty state dir lands in
	// "created"; a successful re-apply after the test runs once but
	// the cleanup didn't fully run can land in "updated".
	out = runTrondCtx(ctx, t, env, "apply", "--intent", intentPath, "--output", "json", "--auto-approve")
	var applyResult map[string]any
	mustUnmarshalJSON(t, out, &applyResult)
	gotResult, _ := applyResult["result"].(string)
	if gotResult != "created" {
		t.Fatalf("apply result: expected created (fresh state-dir), got %q\nbody: %s", gotResult, out)
	}
	// Always try to remove the node, even if a later step fails. The
	// container is real and the developer's docker daemon is global.
	t.Cleanup(func() {
		// Best-effort. If apply itself never created it, this is a
		// no-op; if it did, removing here recovers from any panic
		// path that t.Fatal might have left behind.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_, _ = runTrondAllowFail(cleanupCtx, t, env,
			"remove", "my-fullnode", "--confirm", "my-fullnode", "--output", "json")
	})
	t.Log("apply: OK")

	// 3. Status
	out = runTrondCtx(ctx, t, env, "status", "my-fullnode", "--output", "json")
	var statusResult map[string]any
	mustUnmarshalJSON(t, out, &statusResult)
	if statusResult["status"] != "running" {
		t.Fatalf("status not running: %s", out)
	}
	t.Log("status: OK")

	// 4. List
	out = runTrondCtx(ctx, t, env, "list", "--output", "json")
	var listResult []map[string]any
	mustUnmarshalJSON(t, out, &listResult)
	if len(listResult) == 0 {
		t.Fatal("list returned empty")
	}
	t.Log("list: OK")

	// 5. Stop
	out = runTrondCtx(ctx, t, env, "stop", "my-fullnode", "--output", "json")
	var stopResult map[string]any
	mustUnmarshalJSON(t, out, &stopResult)
	if stopResult["status"] != "stopped" {
		t.Fatalf("stop status not stopped: %s", out)
	}
	t.Log("stop: OK")

	// 6. Start
	out = runTrondCtx(ctx, t, env, "start", "my-fullnode", "--output", "json")
	var startResult map[string]any
	mustUnmarshalJSON(t, out, &startResult)
	if startResult["status"] != "running" {
		t.Fatalf("start status not running: %s", out)
	}
	t.Log("start: OK")

	// 7. Remove
	out = runTrondCtx(ctx, t, env, "remove", "my-fullnode", "--confirm", "my-fullnode", "--output", "json")
	var removeResult map[string]any
	mustUnmarshalJSON(t, out, &removeResult)
	if removeResult["status"] != "removed" {
		t.Fatalf("remove status not removed: %s", out)
	}
	t.Log("remove: OK")

	// 8. Verify list is empty
	out = runTrondCtx(ctx, t, env, "list", "--output", "json")
	var finalList []map[string]any
	mustUnmarshalJSON(t, out, &finalList)
	if len(finalList) != 0 {
		t.Fatalf("list not empty after remove: %s", out)
	}
	t.Log("full lifecycle: PASS")
}
