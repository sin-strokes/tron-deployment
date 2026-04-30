package cmd

import (
	"testing"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

// seedState writes a state.json under a per-test base dir and returns
// nothing — callers use paths.State() to read back. Each test must call
// paths.SetBaseDir("") at end via t.Cleanup so other tests aren't
// affected by the override.
func seedState(t *testing.T, name string, httpPort int) {
	t.Helper()
	dir := t.TempDir()
	paths.SetBaseDir(dir)
	t.Cleanup(func() { paths.SetBaseDir("") })

	store, err := state.NewStore(paths.State())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	st := &state.DeploymentState{
		Version: 1,
		Nodes: []state.ManagedNode{{
			Name:        name,
			Status:      "running",
			Runtime:     "docker",
			LastApplied: time.Now().UTC(),
			HTTPPort:    httpPort,
		}},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestResolveVerifyPort_PrefersState(t *testing.T) {
	seedState(t, "n1", 49231) // OS-allocated under auto_ports
	got := resolveVerifyPort("n1", 0)
	if got != 49231 {
		t.Fatalf("expected port from state (49231), got %d", got)
	}
}

func TestResolveVerifyPort_StateOverridesIntent(t *testing.T) {
	// State and intent disagree (e.g. intent edited after deploy).
	// State wins because that's what's actually running.
	seedState(t, "n1", 12345)
	got := resolveVerifyPort("n1", 8090)
	if got != 12345 {
		t.Fatalf("expected state value (12345), got %d", got)
	}
}

func TestResolveVerifyPort_FallsBackToIntent(t *testing.T) {
	// Node not in state (e.g. running verify before first apply).
	seedState(t, "other-node", 12345)
	got := resolveVerifyPort("n1", 8091)
	if got != 8091 {
		t.Fatalf("expected intent value (8091), got %d", got)
	}
}

func TestResolveVerifyPort_FallsBackTo8090(t *testing.T) {
	// No state file, no intent value: java-tron default.
	dir := t.TempDir()
	paths.SetBaseDir(dir)
	t.Cleanup(func() { paths.SetBaseDir("") })
	got := resolveVerifyPort("n1", 0)
	if got != 8090 {
		t.Fatalf("expected default 8090, got %d", got)
	}
}

func TestResolveVerifyPort_StateZeroFallsBackToIntent(t *testing.T) {
	// Pre-1.0 state file: HTTPPort field absent → 0. We must NOT use 0
	// from state; fall through to intent.
	seedState(t, "n1", 0)
	got := resolveVerifyPort("n1", 8091)
	if got != 8091 {
		t.Fatalf("expected intent value (8091) when state.HTTPPort=0, got %d", got)
	}
}
