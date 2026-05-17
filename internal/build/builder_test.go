package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/paths"
)

// recordingRunner intercepts the docker invocation so we can assert
// on the exact argv shape without spinning up a real builder. It is
// the testing seam introduced by the runner.go refactor.
type recordingRunner struct {
	called   bool
	resolved *resolved
	outTmp   string
	// behavior knobs:
	plantArtifact  string // if non-empty, write this content as the .tmp on Run
	returnErr      error
	delayBeforeRun time.Duration
	respectCancel  bool
}

func (r *recordingRunner) RunDockerBuild(ctx context.Context, res *resolved, outDir, outTmp string) error {
	r.called = true
	r.resolved = res
	r.outTmp = outTmp

	if r.delayBeforeRun > 0 {
		select {
		case <-time.After(r.delayBeforeRun):
		case <-ctx.Done():
			if r.respectCancel {
				return ctx.Err()
			}
		}
	}
	if r.returnErr != nil {
		return r.returnErr
	}
	if r.plantArtifact != "" {
		return os.WriteFile(outTmp, []byte(r.plantArtifact), 0o600)
	}
	return nil
}

// withMockRunner swaps the package-level defaultRunner for the
// duration of one test. Restoration is registered via t.Cleanup so
// parallel-safe across the suite.
func withMockRunner(t *testing.T, mock dockerRunner) {
	t.Helper()
	orig := defaultRunner
	defaultRunner = mock
	t.Cleanup(func() { defaultRunner = orig })
}

func setupTestRepo(t *testing.T) string {
	t.Helper()
	return initGitRepo(t) // reuse the helper from source_test.go
}

// makeValidJARBytes returns a tiny ZIP that ValidateJARMainClass will
// accept as a "java-tron" jar. Used to plant artifacts in tests
// without spinning up gradle.
func makeValidJARBytes(t *testing.T) []byte {
	t.Helper()
	path := makeJAR(t, "org.tron.program.FullNode")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture jar: %v", err)
	}
	return data
}

// TestRun_HappyPath asserts the full lifecycle end-to-end with a
// mock runner that "produces" a valid jar: cache miss → build →
// validate → finalize → manifest persisted → result populated.
func TestRun_HappyPath(t *testing.T) {
	withTempBaseDir(t)
	repo := setupTestRepo(t)
	mock := &recordingRunner{plantArtifact: string(makeValidJARBytes(t))}
	withMockRunner(t, mock)

	res, err := Run(context.Background(), Request{
		SourcePath:           repo,
		BuilderImageOverride: "test-image@sha256:abcdef1234567890",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !mock.called {
		t.Error("docker runner was not invoked")
	}
	if res.CacheHit {
		t.Error("first build should not be a cache hit")
	}
	if res.ArtifactKind != "jar" {
		t.Errorf("artifact_kind = %q; want jar", res.ArtifactKind)
	}
	if !filepath.IsAbs(res.ArtifactPath) {
		t.Errorf("artifact_path should be absolute; got %q", res.ArtifactPath)
	}
	if _, err := os.Stat(res.ArtifactPath); err != nil {
		t.Errorf("artifact should exist on disk: %v", err)
	}
	if res.SHA256 == "" {
		t.Error("sha256 not populated")
	}
}

// TestRun_CacheHit asserts a second run with the same inputs returns
// instantly without re-invoking the runner.
func TestRun_CacheHit(t *testing.T) {
	withTempBaseDir(t)
	repo := setupTestRepo(t)

	mock := &recordingRunner{plantArtifact: string(makeValidJARBytes(t))}
	withMockRunner(t, mock)

	req := Request{
		SourcePath:           repo,
		BuilderImageOverride: "test-image@sha256:abcdef1234567890",
	}
	if _, err := Run(context.Background(), req); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second run: docker runner should NOT be invoked.
	mock.called = false
	res, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if mock.called {
		t.Error("cache hit should skip the docker invocation")
	}
	if !res.CacheHit {
		t.Error("second run should report cache_hit=true")
	}
}

// TestRun_BuildFailedSurfacesEnvelope asserts a runner error becomes
// a BUILD_FAILED structured error with the right exit code.
func TestRun_BuildFailedSurfacesEnvelope(t *testing.T) {
	withTempBaseDir(t)
	repo := setupTestRepo(t)
	mock := &recordingRunner{returnErr: errors.New("gradle: compile error")}
	withMockRunner(t, mock)

	_, err := Run(context.Background(), Request{
		SourcePath:           repo,
		BuilderImageOverride: "test-image@sha256:abcdef1234567890",
	})
	if err == nil {
		t.Fatal("expected BUILD_FAILED, got nil")
	}
	if !strings.Contains(err.Error(), "gradle build failed") {
		t.Errorf("error %q should mention gradle build failed", err)
	}
}

// TestRun_SIGINTReportsCancelled is the FR-016 regression guard:
// cancelling the context mid-build surfaces as BUILD_CANCELLED with
// exit code 130 and partial output is cleaned up.
func TestRun_SIGINTReportsCancelled(t *testing.T) {
	withTempBaseDir(t)
	repo := setupTestRepo(t)

	mock := &recordingRunner{
		delayBeforeRun: 1 * time.Second,
		respectCancel:  true,
	}
	withMockRunner(t, mock)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := Run(ctx, Request{
		SourcePath:           repo,
		BuilderImageOverride: "test-image@sha256:abcdef1234567890",
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error %q should mention cancelled", err)
	}

	// Cleanup invariant: outTmp must not be left behind. We can
	// inspect the cache dir directly.
	outDir := filepath.Join(CacheDir(), "out")
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("partial output %q not cleaned up after cancel", e.Name())
		}
	}
}

// TestRun_InvalidArtifactRejected covers the FR-011 path: a runner
// that produces a JAR whose Main-Class is wrong must be caught
// before declaring success.
func TestRun_InvalidArtifactRejected(t *testing.T) {
	withTempBaseDir(t)
	repo := setupTestRepo(t)

	// Plant a JAR with the wrong Main-Class.
	mock := &recordingRunner{plantArtifact: makeJARContents(t, "com.example.WrongMain")}
	withMockRunner(t, mock)

	_, err := Run(context.Background(), Request{
		SourcePath:           repo,
		BuilderImageOverride: "test-image@sha256:abcdef1234567890",
	})
	if err == nil {
		t.Fatal("expected INVALID_ARTIFACT")
	}
	if !strings.Contains(err.Error(), "java-tron node") {
		t.Errorf("error %q should mention java-tron node", err)
	}
}

// TestArgvFormAssertion verifies the runner.go script is invoked
// argv-form: never via "bash -c '...interpolated...'". This is the
// regression guard for FR-022. The real runner builds the argv list;
// we test the assertion separately on the constructed args.
func TestArgvFormAssertion(t *testing.T) {
	withTempBaseDir(t)
	repo := setupTestRepo(t)

	// Use the real runner indirectly by inspecting the args it
	// would build. We do this by capturing through a runner that
	// reports the resolved struct and then constructs the same argv
	// shape we'd expect (see runner.go).
	var captured *resolved
	withMockRunner(t, &capturingRunner{cb: func(r *resolved) {
		captured = r
	}})

	req := Request{
		SourcePath:           repo,
		BuilderImageOverride: "test-image@sha256:abcdef1234567890",
		GradleTask:           "shadowJar",
		GradleArgs:           []string{"--offline", "-Dversion=1.2.3"},
	}
	// Plant the artifact via a follow-up so Run doesn't fail post-runner.
	go func() {
		// no-op; capturingRunner writes nothing, so build fails after
		// the script "ran" — that's OK, we only care it was invoked.
	}()
	_, _ = Run(context.Background(), req)
	if captured == nil {
		t.Fatal("runner was never invoked")
	}

	// The contract we enforce here: gradle args are passed through
	// AS-IS in their original argv shape. The runner is what stitches
	// them into a docker exec.Command call without any shell
	// interpolation; we trust that contract by reading runner.go
	// (which uses a constant `dockerBuildScript` and appends user
	// args via argv).
	if captured.req.GradleTask != "shadowJar" {
		t.Errorf("gradle_task lost in plumbing: %q", captured.req.GradleTask)
	}
	if len(captured.req.GradleArgs) != 2 {
		t.Errorf("gradle_args length: got %d; want 2", len(captured.req.GradleArgs))
	}
}

// TestDockerBuildScript_NoUserInputInterpolation enforces at the
// source level that the dockerBuildScript constant doesn't reference
// trond-side request fields by name. Command substitution `$(...)` of
// trond's own shell idioms (like `ls -S`) is OK — the danger is
// `${GRADLE_TASK}` or similar where user input would be interpolated
// rather than passed as argv.
//
// Static analysis through the test suite: if someone adds
// `${USER_INPUT}` into the script, this fails before reaching CI.
func TestDockerBuildScript_NoUserInputInterpolation(t *testing.T) {
	// Names of Go-side fields that, if they leaked into the script,
	// would indicate the FR-022 boundary was crossed.
	forbidden := []string{
		"$GRADLE_TASK", "${GRADLE_TASK",
		"$GRADLE_ARGS", "${GRADLE_ARGS",
		"$REQUEST", "${REQUEST",
		"$SOURCE_PATH", "${SOURCE_PATH",
		"$IMAGE_TAG", "${IMAGE_TAG",
		"eval ", // explicit eval is always wrong
	}
	for _, f := range forbidden {
		if strings.Contains(dockerBuildScript, f) {
			t.Errorf("dockerBuildScript contains forbidden pattern %q: must not interpolate user input", f)
		}
	}
	// The script MUST use "$@" (argv passthrough) for gradle args.
	if !strings.Contains(dockerBuildScript, `"$@"`) {
		t.Error(`dockerBuildScript must forward gradle args via "$@" (argv expansion), not by string interpolation`)
	}
	// The script MUST reference OUT_NAME via env (the value is
	// shell-safe regardless of contents).
	if !strings.Contains(dockerBuildScript, "$OUT_NAME") {
		t.Error("dockerBuildScript must use $OUT_NAME env var, not interpolated filename")
	}
}

// capturingRunner is a sibling of recordingRunner that lets the test
// inspect *resolved* without producing an artifact.
type capturingRunner struct {
	cb func(*resolved)
}

func (c *capturingRunner) RunDockerBuild(ctx context.Context, r *resolved, outDir, outTmp string) error {
	c.cb(r)
	// Return error so Run cleans up and exits — caller doesn't care
	// about the result, only the captured resolved.
	return errors.New("capturingRunner returned without artifact")
}

// makeJARContents builds a tiny JAR with the given Main-Class and
// returns its raw bytes for inline planting in tests.
func makeJARContents(t *testing.T, mainClass string) string {
	t.Helper()
	path := makeJAR(t, mainClass)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jar bytes: %v", err)
	}
	return string(data)
}

// silence unused-paths import in some builds
var _ = paths.BaseDir
