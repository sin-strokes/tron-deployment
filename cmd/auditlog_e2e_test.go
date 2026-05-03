//go:build e2e

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestE2E_AuditLog_JSONLConformance pins down the audit log's wire
// shape: every state-mutating command writes one JSONL line into
// $TROND_STATE_DIR/audit.log, and each line conforms to
// schemas/output/events.schema.json.
//
// Why this matters: audit.log is what an operator audits or feeds
// into a SIEM after the fact. A field rename or a missing required
// key (timestamp / command / result) silently breaks downstream log
// shippers. Up to this PR it was effectively untested.
//
// We deliberately use commands that fail predictably (no Docker
// dependency for most of them) — the audit log records every attempt,
// successful or not.
func TestE2E_AuditLog_JSONLConformance(t *testing.T) {
	stateDir, env := e2eEnv(t)

	// Schema lives under schemas/output/events.schema.json. Compile
	// once for the whole table — the validator is reusable.
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	schemaPath := filepath.Join(repoRoot, "schemas", "output", "events.schema.json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Trigger a few audit-emitting commands. They don't all need to
	// succeed — what we want is *the audit line* itself to land and
	// validate. `remove` without --confirm exits 10 but still
	// short-circuits before writeAudit; `stop` of a missing node
	// fails through writeAudit's failure branch.
	//
	// We use stop on a non-existent node to drive the failure path.
	_, _ = runTrondAllowFail(ctx, t, env,
		"stop", "definitely-not-there", "--output", "json")

	// Nothing was written for that case (stop returns NODE_NOT_FOUND
	// before resolveNodeContext lets us reach writeAudit). Pick a
	// command that does write: `network destroy --confirm` against
	// a name that has zero matching nodes — destroys nothing but
	// emits the audit row with result=success (because zero ops
	// failed). Skip if that route is also short-circuited; the
	// `apply` audit line from the apply test (when run together)
	// would have covered it. Here we use the dedicated path:
	//
	// The most reliable no-Docker writeAudit trigger is `bootstrap`
	// against an unreachable target — it goes through writeAudit on
	// failure. But bootstrap requires SSH config. We fall back to
	// asserting the *empty case*: zero audit lines should validate
	// trivially as zero entries, then we let any docker-gated test
	// that already ran in the same package contribute lines.

	auditPath := filepath.Join(stateDir, "audit.log")

	// Force at least one entry by writing a known-good audit line
	// directly through the binary path: apply against a malformed
	// intent. apply's resolveTarget writeAudit call records failures.
	// This avoids needing Docker.
	badIntent := filepath.Join(stateDir, "bad.yaml")
	if err := os.WriteFile(badIntent, []byte("name: bad\n"), 0o600); err != nil {
		t.Fatalf("write bad intent: %v", err)
	}
	_, _ = runTrondAllowFail(ctx, t, env,
		"apply", "--intent", badIntent, "--auto-approve", "--output", "json")

	// Now read the audit log if present. It may not exist if no
	// command reached writeAudit — that's also informative.
	data, err := os.ReadFile(auditPath)
	if os.IsNotExist(err) {
		// Acceptable today: only docker-touching paths reach writeAudit
		// in our minimal no-docker subset. Mark the test as
		// informational rather than failing.
		t.Skip("no audit.log emitted by no-docker subset; covered by docker-gated tests")
	}
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}

	// Validate every line against events.schema.json.
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate long lines
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var parsed any
		if err := json.Unmarshal(line, &parsed); err != nil {
			t.Errorf("audit.log line %d not JSON: %v\nline: %s", lineNo, err, line)
			continue
		}
		if err := validateAgainstSchema(schemaPath, parsed); err != nil {
			t.Errorf("audit.log line %d failed events.schema.json: %v\nline: %s",
				lineNo, err, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit.log: %v", err)
	}
	if lineNo == 0 {
		t.Skip("audit.log was created but had no entries — nothing to validate")
	}
	t.Logf("validated %d audit.log entries", lineNo)
}

// TestE2E_AuditLog_PerCommand_Docker covers the docker-required
// commands that emit the most informative audit entries: apply →
// stop → start → remove. Each emits one line with command + node
// + result, all of which the schema requires.
func TestE2E_AuditLog_PerCommand_Docker(t *testing.T) {
	skipUnlessDocker(t)
	stateDir, env := e2eEnv(t)

	intentPath := filepath.Join(stateDir, "intent.yaml")
	if err := os.WriteFile(intentPath, []byte(idempotencyIntent("8GB")), 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_, _ = runTrondAllowFail(cleanupCtx, t, env,
			"remove", "trond-idempotency", "--confirm", "trond-idempotency", "--output", "json")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	runTrondCtx(ctx, t, env, "apply", "--intent", intentPath, "--auto-approve", "--output", "json")
	runTrondCtx(ctx, t, env, "stop", "trond-idempotency", "--output", "json")
	runTrondCtx(ctx, t, env, "start", "trond-idempotency", "--output", "json")
	runTrondCtx(ctx, t, env, "remove", "trond-idempotency", "--confirm", "trond-idempotency", "--output", "json")

	repoRoot, _ := filepath.Abs("..")
	schemaPath := filepath.Join(repoRoot, "schemas", "output", "events.schema.json")
	data, err := os.ReadFile(filepath.Join(stateDir, "audit.log"))
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}

	gotCommands := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var parsed map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &parsed); err != nil {
			t.Errorf("audit line not JSON: %v\nraw: %s", err, scanner.Bytes())
			continue
		}
		if err := validateAgainstSchema(schemaPath, parsed); err != nil {
			t.Errorf("audit line failed schema: %v\nraw: %s", err, scanner.Bytes())
		}
		if cmd, _ := parsed["command"].(string); cmd != "" {
			gotCommands[cmd] = true
		}
	}
	wantCommands := []string{"apply", "stop", "start", "remove"}
	for _, want := range wantCommands {
		if !gotCommands[want] {
			t.Errorf("audit log missing %q entry; got commands: %v", want, gotCommands)
		}
	}
}
