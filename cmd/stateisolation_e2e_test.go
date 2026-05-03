//go:build e2e

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestE2E_StateIsolation runs a matrix of read-side trond commands
// with TROND_STATE_DIR set to a sandbox and asserts the host's
// developer ~/.trond is never touched.
//
// Why this matters: every helper in cmd/ is supposed to route through
// internal/paths so that --state-dir / TROND_STATE_DIR redirects all
// disk writes. A future helper that hard-codes ~/.trond/something
// would silently pollute the developer's machine. This test would go
// red the moment that happens.
//
// Strategy: snapshot the (existence, mtime) of $HOME/.trond before
// running the matrix; afterwards, assert nothing in that path
// changed. If $HOME/.trond doesn't exist when the test starts, it
// must not exist when the test ends.
func TestE2E_StateIsolation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	hostTrond := filepath.Join(home, ".trond")
	preExisted := false
	var preMtime time.Time
	if info, err := os.Stat(hostTrond); err == nil {
		preExisted = true
		preMtime = info.ModTime()
	}

	_, env := e2eEnv(t)

	commands := [][]string{
		{"version"},
		{"doctor"},
		{"list"},
		{"snapshot", "sources"},
		{"snapshot", "list", "--network", "nile"},
		{"snapshot", "jobs"},
		{"recipe", "list"},
		{"recipe", "show", "nile-test-fullnode"},
		{"network", "status"},
		{"config", "validate", absExample(t, "examples/nile-fullnode.yaml")},
		{"config", "render", absExample(t, "examples/nile-fullnode.yaml")},
		{"plan", "--intent", absExample(t, "examples/nile-fullnode.yaml")},
		{"preflight", "--intent", absExample(t, "examples/nile-fullnode.yaml")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, args := range commands {
		full := append([]string{}, args...)
		full = append(full, "--output", "json")
		// Use AllowFail — a few of these (like snapshot list with
		// no network specified, but we passed it) should succeed;
		// any that don't are still informative for state isolation
		// purposes.
		_, _ = runTrondAllowFail(ctx, t, env, full...)
	}

	// Now check the host's ~/.trond.
	postInfo, err := os.Stat(hostTrond)
	switch {
	case os.IsNotExist(err) && !preExisted:
		// Best case: never existed, still doesn't. Test passes.
	case os.IsNotExist(err) && preExisted:
		// Pre-existed, now gone — would be a bizarre regression
		// (some test path *deleted* the host's state dir). Fail.
		t.Errorf("host ~/.trond existed before the test but is now gone — a trond command appears to have deleted it")
	case err != nil:
		t.Fatalf("stat ~/.trond after test: %v", err)
	case !preExisted:
		t.Errorf("host ~/.trond was created by a trond command despite TROND_STATE_DIR being set\n"+
			"path: %s\nthis means a code path is hard-coding ~/.trond instead of using internal/paths",
			hostTrond)
	case !postInfo.ModTime().Equal(preMtime):
		t.Errorf("host ~/.trond mtime changed (%v → %v) — something wrote inside it",
			preMtime, postInfo.ModTime())
	}
}
