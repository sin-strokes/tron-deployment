//go:build !windows

package build

import (
	"path/filepath"
	"testing"
	"time"
)

// TestAcquireCacheLock_Serializes asserts that two goroutines holding
// the lock for the same key serialize — the second one observes the
// first has released before it acquires. POSIX-only; Windows uses an
// in-process mutex with the same observable behavior (separate test).
func TestAcquireCacheLock_Serializes(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "builds")

	const key = "test-key-abc"
	rel1, err := AcquireCacheLock(cacheDir, key)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired := make(chan time.Time, 1)
	go func() {
		rel2, err := AcquireCacheLock(cacheDir, key)
		if err != nil {
			t.Errorf("second acquire: %v", err)
			return
		}
		acquired <- time.Now()
		rel2()
	}()

	// Hold the lock for a bit, then release.
	time.Sleep(150 * time.Millisecond)
	releaseAt := time.Now()
	rel1()

	select {
	case t2 := <-acquired:
		if t2.Before(releaseAt) {
			t.Errorf("second acquire happened before first release (lock not held): %v vs %v", t2, releaseAt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second goroutine never acquired the lock")
	}
}
