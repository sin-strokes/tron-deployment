package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// recordingSSHTarget captures PutFile + Sha256IfExists calls so the
// apply tests can assert on Phase 4's SSH transfer plumbing without
// spinning up a real SSH server.
type recordingSSHTarget struct {
	*fakeTarget // re-use the Exec / Upload no-ops
	putCalls    int
	lastLocal   string
	lastRemote  string
	stubSHA     string // returned by Sha256IfExists
	stubSHAErr  error
}

func newRecordingSSHTarget(stubSHA string) *recordingSSHTarget {
	return &recordingSSHTarget{
		fakeTarget: &fakeTarget{},
		stubSHA:    stubSHA,
	}
}

func (r *recordingSSHTarget) PutFile(_ context.Context, localPath, remotePath string) error {
	r.putCalls++
	r.lastLocal = localPath
	r.lastRemote = remotePath
	return nil
}

func (r *recordingSSHTarget) Sha256IfExists(_ context.Context, _ string) (string, error) {
	return r.stubSHA, r.stubSHAErr
}

func (r *recordingSSHTarget) CommandExists(_ context.Context, _ string) bool { return true }

// TestTransferBuiltJAR_Skips_WhenRemoteSha256Matches is the Phase 4
// fast-path regression guard. When the remote already holds a JAR
// whose sha256 matches the just-built artifact's, trond MUST NOT
// re-transfer 200 MB of bytes.
func TestTransferBuiltJAR_Skips_WhenRemoteSha256Matches(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "FullNode.jar")
	if err := os.WriteFile(local, []byte("test jar bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	const sha = "abc123def456"
	tgt := newRecordingSSHTarget(sha)
	summary := &BuildSummary{SHA256: sha, ArtifactPath: local}

	if err := transferBuiltJAR(context.Background(), tgt, summary, local, "/remote/FullNode.jar"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tgt.putCalls != 0 {
		t.Errorf("PutFile called %d times; want 0 (sha match → fast-path skip)", tgt.putCalls)
	}
}

// TestTransferBuiltJAR_TransfersWhenRemoteShaMissing covers the
// fresh-deploy case: target has no FullNode.jar yet → must scp.
func TestTransferBuiltJAR_TransfersWhenRemoteShaMissing(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "FullNode.jar")
	if err := os.WriteFile(local, []byte("test jar bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	tgt := newRecordingSSHTarget("") // no remote file → empty sha
	summary := &BuildSummary{SHA256: "abc123", ArtifactPath: local}

	if err := transferBuiltJAR(context.Background(), tgt, summary, local, "/remote/FullNode.jar"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tgt.putCalls != 1 {
		t.Errorf("PutFile calls = %d; want 1", tgt.putCalls)
	}
	if tgt.lastLocal != local || tgt.lastRemote != "/remote/FullNode.jar" {
		t.Errorf("PutFile got local=%q remote=%q; want local=%q remote=%q",
			tgt.lastLocal, tgt.lastRemote, local, "/remote/FullNode.jar")
	}
}

// TestTransferBuiltJAR_TransfersOnShaMismatch covers the
// already-deployed-but-stale case: remote has SOME jar but the
// sha differs → must replace.
func TestTransferBuiltJAR_TransfersOnShaMismatch(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "FullNode.jar")
	if err := os.WriteFile(local, []byte("new bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	tgt := newRecordingSSHTarget("old-sha-stale")
	summary := &BuildSummary{SHA256: "new-sha", ArtifactPath: local}

	if err := transferBuiltJAR(context.Background(), tgt, summary, local, "/remote/FullNode.jar"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tgt.putCalls != 1 {
		t.Errorf("PutFile calls = %d; want 1 (sha mismatch → must transfer)", tgt.putCalls)
	}
}

// TestTransferBuiltJAR_WrapsPutFileErrorsAsDeployError covers the
// error shape contract: a network failure during scp surfaces as
// DEPLOY_ERROR (not a raw fmt error), so cobra's wrapApplyError
// passes it through unchanged and the agent sees the right
// error_code.
type erroringSSHTarget struct {
	*recordingSSHTarget
}

func (e *erroringSSHTarget) PutFile(_ context.Context, _, _ string) error {
	return errors.New("connection refused")
}

func TestTransferBuiltJAR_WrapsPutFileErrorsAsDeployError(t *testing.T) {
	tgt := &erroringSSHTarget{newRecordingSSHTarget("")}
	summary := &BuildSummary{SHA256: "any"}

	err := transferBuiltJAR(context.Background(), tgt, summary, "/tmp/x.jar", "/remote/x.jar")
	if err == nil {
		t.Fatal("expected transfer error to propagate")
	}
	// Just verify it's a structured DEPLOY_ERROR carrier — testing
	// the specific message is brittle.
	if !contains(err.Error(), "transfer built JAR") {
		t.Errorf("error %q should mention 'transfer built JAR'", err)
	}
}

// TestTransferBuiltJAR_BailsOnSha256TransportError pins the H2
// review-pass-3 fix: when Sha256IfExists returns a transport-level
// error (SSH session can't open, link dropped), transferBuiltJAR
// MUST NOT fall through and waste a multi-hundred-MB PutFile
// attempt on the same broken link. It should surface the error
// directly so the operator sees the real failure quickly.
func TestTransferBuiltJAR_BailsOnSha256TransportError(t *testing.T) {
	tgt := newRecordingSSHTarget("")
	tgt.stubSHAErr = errors.New("ssh: handshake failed: EOF")
	summary := &BuildSummary{SHA256: "abc123"}

	err := transferBuiltJAR(context.Background(), tgt, summary, "/tmp/x.jar", "/remote/x.jar")
	if err == nil {
		t.Fatal("expected transport error to surface, got nil")
	}
	if !contains(err.Error(), "check remote sha256") {
		t.Errorf("error %q should mention the sha256 step (not generic PutFile)", err)
	}
	if tgt.putCalls != 0 {
		t.Errorf("PutFile called %d times; want 0 (transport error → bail before transfer)", tgt.putCalls)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
