package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestListJobs_HandlesFutureStartedAt verifies that a job written
// with a StartedAt in the future (clock skew, manual edit, restored
// backup) does not break ListJobs sorting. Earlier this would put
// the future job at the top and silently mask all the real recent
// activity.
func TestListJobs_HandlesFutureStartedAt(t *testing.T) {
	dir := t.TempDir()

	// Plant three manifests: two "normal", one in the future.
	must := func(j Job) {
		t.Helper()
		if err := WriteJob(dir, j); err != nil {
			t.Fatalf("WriteJob: %v", err)
		}
	}
	now := time.Now().UTC()
	must(Job{ID: "old", PID: 1, StartedAt: now.Add(-2 * time.Hour)})
	must(Job{ID: "mid", PID: 2, StartedAt: now.Add(-1 * time.Hour)})
	must(Job{ID: "future", PID: 3, StartedAt: now.Add(+1 * time.Hour)})

	jobs, err := ListJobs(dir)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	// Sort order is newest-first by StartedAt — "future" comes
	// first; the test asserts the function doesn't panic / drop a
	// job, regardless of skew.
	if jobs[0].ID != "future" {
		t.Errorf("expected future first, got %s", jobs[0].ID)
	}
	if jobs[2].ID != "old" {
		t.Errorf("expected old last, got %s", jobs[2].ID)
	}
}

// TestRemoveJob_LogAndManifest verifies that RemoveJob removes both
// the .json and .log files. Half-removed jobs (manifest gone, log
// remaining) would leak disk space across upgrades.
func TestRemoveJob_LogAndManifest(t *testing.T) {
	dir := t.TempDir()

	if err := WriteJob(dir, Job{ID: "x", PID: 1, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteJob: %v", err)
	}
	logPath := filepath.Join(dir, "x.log")
	if err := os.WriteFile(logPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if err := RemoveJob(dir, "x"); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.json")); !os.IsNotExist(err) {
		t.Errorf("manifest not removed: %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("log not removed: %v", err)
	}
}
