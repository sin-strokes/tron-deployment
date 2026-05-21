package apply

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

// fakeTarget satisfies target.Target with no-op stubs. Apply only
// exercises Exec (for the JDK probe + docker deploy fan-out); the
// other methods exist to satisfy the interface contract.
type fakeTarget struct{}

func (f *fakeTarget) Exec(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return nil, nil
}
func (f *fakeTarget) Upload(_ context.Context, _, _ string) error          { return nil }
func (f *fakeTarget) Download(_ context.Context, _, _ string) error        { return nil }
func (f *fakeTarget) ReadFile(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (f *fakeTarget) WriteFile(_ context.Context, _ string, _ []byte, _ os.FileMode) error {
	return nil
}
func (f *fakeTarget) DiskFree(_ context.Context, _ string) (uint64, error) { return 1 << 40, nil }
func (f *fakeTarget) MemTotal(_ context.Context) (uint64, error)           { return 1 << 30, nil }
func (f *fakeTarget) PutFile(_ context.Context, _, _ string) error         { return nil }
func (f *fakeTarget) Sha256IfExists(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *fakeTarget) CommandExists(_ context.Context, _ string) bool { return true }
func (f *fakeTarget) String() string                                 { return "fake" }

func TestApply_NoOpWhenIntentHashMatches(t *testing.T) {
	parsed := minimalIntent()
	store, st := freshStore(t)
	existing := state.ManagedNode{
		Name:        parsed.Name,
		IntentHash:  "deadbeef",
		ConfigHash:  "cafef00d",
		Version:     "4.8.1",
		Runtime:     "docker",
		Status:      "running",
		LastApplied: time.Now(),
	}
	store.UpsertNode(st, existing)

	res, err := Apply(context.Background(), Options{
		Intent:     parsed,
		Target:     &fakeTarget{},
		Store:      store,
		State:      st,
		IntentHash: "deadbeef",
		Existing:   &existing,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Outcome != "no_change" {
		t.Errorf("Outcome = %s, want no_change", res.Outcome)
	}
	if res.Name != parsed.Name {
		t.Errorf("Name = %s, want %s", res.Name, parsed.Name)
	}
}

func TestApply_RequiresIntent(t *testing.T) {
	if _, err := Apply(context.Background(), Options{}); err == nil {
		t.Error("expected error when Intent is nil")
	}
}

func TestApply_RequiresIntentHash(t *testing.T) {
	parsed := minimalIntent()
	store, st := freshStore(t)
	if _, err := Apply(context.Background(), Options{
		Intent: parsed,
		Target: &fakeTarget{},
		Store:  store,
		State:  st,
	}); err == nil {
		t.Error("expected error when IntentHash is empty")
	}
}

func TestIntentHashFromBytes_Stable(t *testing.T) {
	// Lower-case hex of SHA256("hello world\n") — sanity check the
	// alias matches sha256 stdlib behavior.
	got := IntentHashFromBytes([]byte("hello world\n"))
	want := "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447"
	if got != want {
		t.Errorf("hash mismatch: got %s, want %s", got, want)
	}
}

// --- helpers ---

func minimalIntent() *intent.Intent {
	return &intent.Intent{
		Name:    "test-node",
		Network: "nile",
		Target:  intent.Target{Type: "local", Runtime: "docker"},
		Nodes: []intent.NodeSpec{{
			Type:      "fullnode",
			Version:   "4.8.1",
			Resources: intent.Resources{Memory: "8GB"},
			Ports:     intent.PortMapping{HTTP: 8090, GRPC: 50051},
		}},
	}
}

func freshStore(t *testing.T) (*state.Store, *state.DeploymentState) {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return store, st
}
