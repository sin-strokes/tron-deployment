//go:build e2e

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// applySchemaPath resolves to schemas/output/apply.schema.json, used by
// the test to fail loud if any apply branch (created / no_change /
// updated) emits a payload that violates the published contract — in
// particular, additionalProperties:false catches stray fields the way
// the old `status` / `changes` keys would have been caught earlier.
func applySchemaPath(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return filepath.Join(repoRoot, "schemas", "output", "apply.schema.json")
}

// TestE2E_Apply_IdempotencyAndGate pins down the two safety contracts
// trond's apply command makes to AI agents:
//
//  1. **Idempotency.** Running `apply` twice with the same intent
//     must not redeploy. The second call returns outcome=no_change
//     instantly. Without this contract, an agent looping over a
//     desired-state declaration would burn CPU and rotate
//     containers on every iteration.
//
//  2. **Human-required gate.** Once a node exists, any further
//     `apply` that would change its config MUST exit 10
//     (HUMAN_REQUIRED) unless --auto-approve is passed. Without
//     this gate, an agent making a typo in the intent could
//     silently restart a production node.
//
// We exercise both via a real local docker target — these contracts
// are guarded only at the cmd/apply.go layer, not internal/apply
// (which only handles the no_change short-circuit). Mocking the
// runtime would skip the very layer the test is meant to cover.
//
// Skipped when Docker isn't reachable.
func TestE2E_Apply_IdempotencyAndGate(t *testing.T) {
	skipUnlessDocker(t)

	stateDir, env := e2eEnv(t)

	// Stage a copy of the Nile intent under a unique name so this
	// test doesn't collide with other e2e tests' nodes (the lifecycle
	// test already manages `nile-fullnode`).
	intentPath := filepath.Join(stateDir, "intent.yaml")
	if err := os.WriteFile(intentPath, []byte(idempotencyIntent("8GB")), 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// Always tear the container down at the end of the test, even on
	// fatal — the developer's docker daemon is shared.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_, _ = runTrondAllowFail(cleanupCtx, t, env,
			"remove", "trond-idempotency", "--confirm", "trond-idempotency", "--output", "json")
	})

	// 1. First apply — should create the node. With a fresh
	//    TROND_STATE_DIR there's no prior state, so result MUST be
	//    "created" exactly. Anything else means state isolation is
	//    broken (the test inherited state from elsewhere) and the
	//    rest of the flow would assert on false premises.
	out := runTrondCtx(ctx, t, env,
		"apply", "--intent", intentPath, "--auto-approve", "--output", "json")
	first := mustParseApply(t, out)
	if first.Result != "created" {
		t.Fatalf("first apply: expected result=created (state-dir is fresh), got %q\nbody: %s",
			first.Result, out)
	}
	firstHash := first.IntentHash
	if firstHash == "" {
		t.Fatalf("first apply: intent_hash missing\nbody: %s", out)
	}
	t.Logf("first apply: result=%s hash=%s", first.Result, firstHash)

	// 2. Second apply with the *same* intent — must be a no-op.
	out = runTrondCtx(ctx, t, env,
		"apply", "--intent", intentPath, "--auto-approve", "--output", "json")
	second := mustParseApply(t, out)
	if second.Result != "no_change" {
		t.Errorf("second apply (unchanged intent): expected result=no_change, got %q\nbody: %s",
			second.Result, out)
	}
	if second.IntentHash != firstHash {
		t.Errorf("second apply: intent_hash changed without intent change (%s vs %s)",
			second.IntentHash, firstHash)
	}

	// 3. Mutate the intent (memory tweak — config-affecting,
	//    runtime-safe). Now apply WITHOUT --auto-approve must exit 10.
	if err := os.WriteFile(intentPath, []byte(idempotencyIntent("4GB")), 0o600); err != nil {
		t.Fatalf("rewrite intent: %v", err)
	}
	out, err := runTrondAllowFail(ctx, t, env,
		"apply", "--intent", intentPath, "--output", "json")
	gotExit := exitCodeOf(err)
	if gotExit != 10 {
		t.Fatalf("changed intent without --auto-approve: expected exit=10 (HUMAN_REQUIRED), got %d\nbody: %s",
			gotExit, out)
	}
	var envelope struct {
		ErrorCode string `json:"error_code"`
		ExitCode  int    `json:"exit_code"`
	}
	if jsonErr := json.Unmarshal(out, &envelope); jsonErr != nil {
		t.Fatalf("error envelope not JSON: %v\nbody: %s", jsonErr, out)
	}
	if envelope.ErrorCode != "HUMAN_REQUIRED" {
		t.Errorf("error_code: got %q, want HUMAN_REQUIRED\nbody: %s", envelope.ErrorCode, out)
	}
	if envelope.ExitCode != 10 {
		t.Errorf("envelope exit_code: got %d, want 10\nbody: %s", envelope.ExitCode, out)
	}

	// 4. Re-apply with --auto-approve. Now must succeed and report a
	//    different intent_hash. result must be "updated" (real
	//    change applied) for a memory edit.
	out = runTrondCtx(ctx, t, env,
		"apply", "--intent", intentPath, "--auto-approve", "--output", "json")
	updated := mustParseApply(t, out)
	if updated.Result != "updated" {
		t.Errorf("re-apply with --auto-approve: expected result=updated, got %q\nbody: %s",
			updated.Result, out)
	}
	if updated.IntentHash == firstHash {
		t.Errorf("intent_hash should change after intent edit; got identical %s", updated.IntentHash)
	}
}

// applyResult is the subset of `trond apply -o json` we assert on.
// The full schema (schemas/output/apply.schema.json) carries more
// fields; this test only cares about the contract-critical ones.
//
// Wire field is "result" (per schema + AGENTS.md). Inside
// internal/apply the Go field is named Outcome.
type applyResult struct {
	Result     string `json:"result"`
	IntentHash string `json:"intent_hash"`
	Name       string `json:"name"`
}

func mustParseApply(t *testing.T, out []byte) applyResult {
	t.Helper()
	// 1. Full schema validation. apply.schema.json sets
	//    additionalProperties:false, so anything trond emits beyond
	//    the documented field set fails here. This is the regression
	//    guard against the `status`/`changes` drift we just fixed —
	//    if it sneaks back in, this test goes red.
	var raw any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("apply output not JSON: %v\nbody: %s", err, out)
	}
	if err := validateAgainstSchema(applySchemaPath(t), raw); err != nil {
		t.Fatalf("apply output failed apply.schema.json: %v\nbody: %s", err, out)
	}
	// 2. Decode the contract-critical subset for the idempotency
	//    assertions further up the call site.
	var r applyResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("apply output not JSON (subset decode): %v\nbody: %s", err, out)
	}
	return r
}

// idempotencyIntent returns a Nile-fullnode intent string with the
// given memory value. Memory is the cleanest field to mutate for
// this test — it changes the rendered HOCON / compose and therefore
// the intent hash, but doesn't break the container's ability to
// start. Other fields like ports would also work but risk port
// collisions with other tests.
func idempotencyIntent(memory string) string {
	return strings.ReplaceAll(`name: trond-idempotency

target:
  type: local
  runtime: docker

network: nile

nodes:
  - type: fullnode
    version: latest
    features:
      jsonrpc: true
    resources:
      memory: __MEMORY__
    ports:
      http: 18190
      grpc: 50151
      jsonrpc: 18545
      p2p: 28888
`, "__MEMORY__", memory)
}
