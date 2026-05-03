//go:build e2e

package cmd

import (
	"context"
	"testing"
	"time"
)

// TestE2E_Network_PrivateLifecycle exercises the multi-node path
// against a real local Docker daemon:
//
//	create → status (2 nodes) → destroy → status (0 nodes)
//
// Uses examples/private-network.yaml (1 witness + 1 fullnode) so the
// test driver also covers the witness-key env-var resolution path
// (the validator rejects raw keys; SR_PRIVATE_KEY must be an env
// name, and we set the env to a deterministic hex value here).
//
// Skipped when Docker isn't reachable. Owns its own TROND_STATE_DIR
// so the run is isolated from any developer-local state.
func TestE2E_Network_PrivateLifecycle(t *testing.T) {
	skipUnlessDocker(t)

	_, env := e2eEnv(t)
	// 32-byte hex — passes the intent validator. Not a real key; the
	// network never produces blocks meaningfully here, the test just
	// wants the create+destroy lifecycle to succeed.
	env = append(env, "SR_PRIVATE_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	intentPath := absExample(t, "examples/private-network.yaml")

	// Generous budget: 2 containers, possible cold-cache image pull.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// Create
	out := runTrondCtx(ctx, t, env, "network", "create", "--intent", intentPath, "--output", "json")
	var createResult map[string]any
	mustUnmarshalJSON(t, out, &createResult)
	// network create's output schema documents `network` + `nodes[]`.
	// Spot-check the two we care about.
	if name, _ := createResult["network"].(string); name != "private-dev" {
		t.Errorf("create: expected network=private-dev, got %v", createResult["network"])
	}
	nodes, _ := createResult["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("create: expected 2 nodes, got %d: %s", len(nodes), out)
	}

	// Always tear down even if a later assertion fails — the
	// containers and docker volumes are real and the developer's
	// docker daemon is shared.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cleanupCancel()
		_, _ = runTrondAllowFail(cleanupCtx, t, env,
			"network", "destroy", "--confirm", "private-dev", "--output", "json")
	})

	// Status — every -node entry should belong to private-dev. We don't
	// assert on `status` per node (a freshly-started witness can
	// briefly be in "starting") — only that the count is right.
	//
	// On resource-constrained CI runners (GitHub free) the second node
	// occasionally takes a few seconds to surface in state.json after
	// `network create` returned. Retry briefly before giving up.
	var statusList []map[string]any
	statusDeadline := time.Now().Add(15 * time.Second)
	for {
		out = runTrondCtx(ctx, t, env, "network", "status", "--output", "json")
		statusList = nil
		mustUnmarshalJSON(t, out, &statusList)
		if len(statusList) == 2 || time.Now().After(statusDeadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(statusList) != 2 {
		t.Fatalf("status: expected 2 nodes after 15s of retry, got %d: %s",
			len(statusList), out)
	}

	// Destroy
	out = runTrondCtx(ctx, t, env, "network", "destroy", "--confirm", "private-dev", "--output", "json")
	var destroyResult map[string]any
	mustUnmarshalJSON(t, out, &destroyResult)
	removed, _ := destroyResult["removed"].([]any)
	if len(removed) != 2 {
		t.Errorf("destroy: expected 2 nodes removed, got %d: %s", len(removed), out)
	}

	// Final status should be empty.
	out = runTrondCtx(ctx, t, env, "network", "status", "--output", "json")
	var finalList []map[string]any
	mustUnmarshalJSON(t, out, &finalList)
	if len(finalList) != 0 {
		t.Fatalf("status post-destroy: expected 0 nodes, got %d: %s", len(finalList), out)
	}
}
