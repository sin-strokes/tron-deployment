package snapshot

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
)

// pruneFixture seeds a per-test snapshots dir with a few job manifests
// at known ages, returns the dir, and registers cleanup. The PIDs are
// chosen as 1 (init, always alive) for "fake-running" jobs and a
// likely-unused high PID for "fake-stopped" so kill(0) returns ESRCH.
func pruneFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paths.SetBaseDir(dir)
	t.Cleanup(func() { paths.SetBaseDir("") })

	jobsDir := paths.SnapshotJobs()
	if err := os.MkdirAll(jobsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Three fixtures:
	//   old-stopped   : PID 999999 (gone), started 30d ago        — should prune
	//   recent-stopped: PID 999998 (gone), started 1h ago         — too young
	//   running       : PID 1 (init, always alive), started 30d   — never prune
	type fix struct {
		id     string
		pid    int
		ageDur time.Duration
	}
	fixtures := []fix{
		{"old-stopped", 999_999, 30 * 24 * time.Hour},
		{"recent-stopped", 999_998, 1 * time.Hour},
		{"running", 1, 30 * 24 * time.Hour},
	}
	for _, f := range fixtures {
		j := snapshot.Job{
			ID:        f.id,
			PID:       f.pid,
			StartedAt: time.Now().Add(-f.ageDur),
			LogPath:   filepath.Join(jobsDir, f.id+".log"),
			Network:   "mainnet",
			Kind:      "lite",
		}
		if err := snapshot.WriteJob(jobsDir, j); err != nil {
			t.Fatal(err)
		}
		// 100-byte log file so reclaimed-bytes is non-zero.
		if err := os.WriteFile(j.LogPath, make([]byte, 100), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return jobsDir
}

func TestPrune_DefaultPolicyKeepsRunningAndYoung(t *testing.T) {
	jobsDir := pruneFixture(t)

	// Default: --older-than 7d, no --all, not dry-run.
	pruneOlderThan = 7 * 24 * time.Hour
	pruneAll = false
	pruneDryRun = false

	if err := runPrune(pruneCmd, nil); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	// old-stopped should be gone; the others should remain.
	mustExist := func(id string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(jobsDir, id+".json")); err != nil {
			t.Errorf("expected %s manifest to remain, got %v", id, err)
		}
	}
	mustNotExist := func(id string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(jobsDir, id+".json")); !os.IsNotExist(err) {
			t.Errorf("expected %s manifest to be removed, got err=%v", id, err)
		}
	}
	mustNotExist("old-stopped")
	mustExist("recent-stopped") // too young
	mustExist("running")        // PID 1 is alive, never prune
}

func TestPrune_AllRemovesYoungToo(t *testing.T) {
	jobsDir := pruneFixture(t)

	pruneOlderThan = 0
	pruneAll = true
	pruneDryRun = false

	if err := runPrune(pruneCmd, nil); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	// Both stopped jobs gone; running still here.
	for _, id := range []string{"old-stopped", "recent-stopped"} {
		if _, err := os.Stat(filepath.Join(jobsDir, id+".json")); !os.IsNotExist(err) {
			t.Errorf("expected %s removed under --all, got err=%v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(jobsDir, "running.json")); err != nil {
		t.Errorf("expected running manifest preserved, got %v", err)
	}
}

func TestPrune_DryRunRemovesNothing(t *testing.T) {
	jobsDir := pruneFixture(t)

	pruneOlderThan = 7 * 24 * time.Hour
	pruneAll = false
	pruneDryRun = true

	if err := runPrune(pruneCmd, nil); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	// All three manifests still on disk.
	for _, id := range []string{"old-stopped", "recent-stopped", "running"} {
		if _, err := os.Stat(filepath.Join(jobsDir, id+".json")); err != nil {
			t.Errorf("expected %s manifest preserved (dry-run), got %v", id, err)
		}
	}
}

func TestPrune_JSONOutputShape(t *testing.T) {
	pruneFixture(t)

	pruneOlderThan = 7 * 24 * time.Hour
	pruneAll = false
	pruneDryRun = true

	// Set the cobra `--output json` flag on the command so the runPrune
	// JSON branch fires. If the persistent flag wasn't inherited (tests
	// don't run cobra's setup), register it locally for this test.
	if pruneCmd.Flags().Lookup("output") == nil {
		pruneCmd.Flags().String("output", "", "test-only")
	}
	if err := pruneCmd.Flags().Set("output", "json"); err != nil {
		t.Fatalf("set output flag: %v", err)
	}
	t.Cleanup(func() { _ = pruneCmd.Flags().Set("output", "") })

	// Capture stdout via a pipe so we can decode the JSON the runPrune
	// helper writes there. Simplest approach without refactoring runPrune
	// to accept an io.Writer.
	r, w, _ := os.Pipe()
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	runErr := runPrune(pruneCmd, nil)
	w.Close()

	body, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("runPrune: %v\noutput: %s", runErr, body)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, body)
	}
	for _, k := range []string{"jobs", "removed_count", "reclaimed_bytes", "dry_run"} {
		if _, ok := got[k]; !ok {
			t.Errorf("expected key %q in JSON output, got %v", k, got)
		}
	}
	if got["dry_run"] != true {
		t.Errorf("expected dry_run=true, got %v", got["dry_run"])
	}
	jobs, ok := got["jobs"].([]any)
	if !ok || len(jobs) != 3 {
		t.Errorf("expected 3 jobs in output, got %v (type %T)", got["jobs"], got["jobs"])
	}
}
