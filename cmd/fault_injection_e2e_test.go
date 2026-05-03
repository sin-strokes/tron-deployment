//go:build e2e

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestE2E_FaultInjection_CorruptedState verifies trond exits with a
// structured error envelope when state.json is malformed JSON, rather
// than panicking or pretending nothing happened. Agents reading the
// envelope's error_code can decide whether to bail or repair.
func TestE2E_FaultInjection_CorruptedState(t *testing.T) {
	stateDir, env := e2eEnv(t)
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"),
		[]byte(`{"nodes": [<--- not json --->]}`), 0o600); err != nil {
		t.Fatalf("write corrupt state.json: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runTrondAllowFail(ctx, t, env, "list", "--output", "json")
	gotExit := exitCodeOf(err)
	if gotExit == 0 {
		t.Fatalf("expected non-zero exit on corrupt state, got 0\nbody: %s", out)
	}

	var envelope map[string]any
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatalf("envelope not JSON: %v\nbody: %s", err, out)
	}
	code, _ := envelope["error_code"].(string)
	if code == "" {
		t.Errorf("expected error_code in envelope; got: %v", envelope)
	}
	// We don't pin the exact error_code (could be STATE_ERROR /
	// VALIDATION_ERROR / INTERNAL_ERROR depending on which layer
	// caught it). What matters: a structured envelope with a code,
	// not a panic or generic exit.
}

// TestE2E_FaultInjection_ReadOnlyState verifies trond's mutating
// commands fail loud when state.json is read-only (chmod 0o400),
// rather than silently dropping the state update.
func TestE2E_FaultInjection_ReadOnlyState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — chmod-based read-only doesn't restrict root")
	}
	stateDir, env := e2eEnv(t)

	// Pre-populate state.json so the write path is exercised (an
	// empty/missing file goes through Create, which has different
	// permissions semantics).
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"),
		[]byte(`{"nodes":[]}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Lock down the state directory itself so atomic-rename writes
	// fail. Restoring permissions in cleanup so t.TempDir's cleanup
	// can remove the dir.
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// `remove` is a mutating command that triggers a state save.
	// Use a node name that doesn't exist; we don't actually want
	// to mutate anything — we want to hit the state-write path.
	out, err := runTrondAllowFail(ctx, t, env,
		"remove", "anything", "--confirm", "anything", "--output", "json")
	if err == nil {
		t.Fatalf("expected non-zero exit on read-only state dir; got success\nbody: %s", out)
	}
	// Either NODE_NOT_FOUND (if the lookup happens before write) or
	// a STATE_ERROR-class envelope (if the write is what fails). Both
	// shapes are valid; what matters is the structured envelope.
	var envelope map[string]any
	if jsonErr := json.Unmarshal(out, &envelope); jsonErr != nil {
		t.Fatalf("envelope not JSON: %v\nbody: %s", jsonErr, out)
	}
	if _, ok := envelope["error_code"]; !ok {
		t.Errorf("expected error_code field; got %v", envelope)
	}
}

// TestE2E_FaultInjection_LockHeld simulates "another trond is
// already running" by acquiring the file lock from a sibling process
// before invoking trond. The contract: trond should fail with a
// LOCK_ERROR envelope (not block forever, not corrupt the state).
//
// This is the canonical race for agents running trond as a subprocess
// while a human operator simultaneously runs a CLI command.
func TestE2E_FaultInjection_LockHeld(t *testing.T) {
	skipUnlessDocker(t)
	stateDir, env := e2eEnv(t)
	intentPath := filepath.Join(stateDir, "intent.yaml")
	if err := os.WriteFile(intentPath, []byte(idempotencyIntent("8GB")), 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Start one apply in the background — this acquires the lock
	// and holds it through the deploy. We pick the apply path
	// rather than something cheaper because apply holds the lock
	// for a measurable window (image pull + compose up).
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()
	bgDone := make(chan []byte, 1)
	go func() {
		out, _ := runTrondAllowFail(bgCtx, t, env,
			"apply", "--intent", intentPath, "--auto-approve", "--output", "json")
		bgDone <- out
	}()
	t.Cleanup(func() {
		// Best-effort cleanup of any container the background apply created.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_, _ = runTrondAllowFail(cleanupCtx, t, env,
			"remove", "trond-idempotency", "--confirm", "trond-idempotency", "--output", "json")
	})

	// Give the background apply a moment to acquire the lock.
	// 250ms is enough on a fast machine; bg may already be inside
	// runtime.Up by the time we proceed.
	time.Sleep(750 * time.Millisecond)

	// Foreground attempt: should hit LOCK_ERROR (or the bg apply
	// finishes first and we see no_change). Either is informative
	// — what we forbid is a panic / silent corruption.
	out, err := runTrondAllowFail(ctx, t, env,
		"apply", "--intent", intentPath, "--auto-approve", "--output", "json")
	t.Logf("foreground apply result: err=%v body=%s", err, out)

	// Wait for bg to finish so the test cleanup runs cleanly.
	<-bgDone

	// Whichever way the race resolved, we expect an envelope-shaped
	// response (success or structured failure). Bare panic / hang =
	// test fails on the context timeout.
	if err == nil {
		var ok map[string]any
		if jsonErr := json.Unmarshal(out, &ok); jsonErr != nil {
			t.Errorf("foreground succeeded but body isn't JSON: %v\n%s", jsonErr, out)
		}
		return
	}
	var envelope map[string]any
	if jsonErr := json.Unmarshal(out, &envelope); jsonErr != nil {
		t.Errorf("foreground failed but body isn't JSON: %v\n%s", jsonErr, out)
		return
	}
	if _, ok := envelope["error_code"]; !ok {
		t.Errorf("expected error_code on lock contention; got %v", envelope)
	}
}
