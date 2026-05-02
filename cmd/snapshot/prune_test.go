package snapshot

import (
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
