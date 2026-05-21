package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveHostIdentity pins the contract `realHostRunner` relies
// on: the function returns a stable digest with the canonical
// `sha256:` prefix + a human-readable ref starting with `host:`. Two
// calls in quick succession yield identical results (java -version
// output is deterministic on a given host) so the cache key is
// stable across same-host re-runs.
//
// Skipped when `java` isn't on PATH (CI runner without a JDK
// shouldn't fail on a host-builder unit test).
func TestResolveHostIdentity(t *testing.T) {
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not on PATH; host builder identity test needs a JVM")
	}

	ref1, digest1, err := resolveHostIdentity(context.Background())
	if err != nil {
		t.Fatalf("resolveHostIdentity: %v", err)
	}
	if !strings.HasPrefix(ref1, "host:") {
		t.Errorf("ref %q should start with 'host:' so it's visibly distinct from a docker ref", ref1)
	}
	if !strings.HasPrefix(digest1, "sha256:") {
		t.Errorf("digest %q should start with 'sha256:' (canonical OCI form)", digest1)
	}
	// Hex-suffix length: 64 chars for full sha256.
	if got := len(strings.TrimPrefix(digest1, "sha256:")); got != 64 {
		t.Errorf("digest hex length = %d; want 64 (sha256)", got)
	}

	// Stability: a second call returns the same digest. If this fails
	// the cache key would also flap and trond would never hit cache
	// for a host build.
	_, digest2, err := resolveHostIdentity(context.Background())
	if err != nil {
		t.Fatalf("second resolveHostIdentity: %v", err)
	}
	if digest1 != digest2 {
		t.Errorf("host identity digest drifted between calls: %q vs %q (cache would never hit)", digest1, digest2)
	}
}

// TestFindLargestFatJAR is the unit-test for the host runner's
// equivalent of the docker script's `find ... | xargs ls -S |
// head -n1`. The largest jar under any `*/build/libs/*.jar` wins.
func TestFindLargestFatJAR(t *testing.T) {
	srcDir := t.TempDir()

	// Multi-module layout, two candidates:
	//   framework/build/libs/FullNode.jar  (1 KB, the fat jar)
	//   common/build/libs/common.jar       (100 B, thin module)
	// Plus a decoy under a path that doesn't match the glob.
	mkJAR := func(rel string, size int) {
		t.Helper()
		p := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mkJAR("framework/build/libs/FullNode.jar", 1024)
	mkJAR("common/build/libs/common.jar", 100)
	mkJAR("not-build/not-libs/decoy.jar", 9999) // largest, but wrong path → must NOT be picked

	got, err := findLargestFatJAR(srcDir)
	if err != nil {
		t.Fatalf("findLargestFatJAR: %v", err)
	}
	want := filepath.Join(srcDir, "framework/build/libs/FullNode.jar")
	if got != want {
		t.Errorf("findLargestFatJAR returned %q; want %q (fat jar in correct path)", got, want)
	}
}

// TestFindLargestFatJAR_NoMatches: when gradle produced nothing
// under */build/libs/*.jar, return a specific error so the caller
// can surface the right BUILD_FAILED message.
func TestFindLargestFatJAR_NoMatches(t *testing.T) {
	srcDir := t.TempDir()
	_, err := findLargestFatJAR(srcDir)
	if err == nil {
		t.Fatal("expected error when no jars present")
	}
	if !strings.Contains(err.Error(), "no .jar") {
		t.Errorf("error %q should mention 'no .jar' so the message is actionable", err)
	}
}

// TestHostRunner_RequiresGradlew: if the source tree doesn't ship a
// ./gradlew wrapper, the runner refuses up-front with a clear
// message rather than silently falling back to whatever `gradle`
// the host's PATH has. Pins the contract documented in the runner.
func TestHostRunner_RequiresGradlew(t *testing.T) {
	srcDir := t.TempDir()
	// Intentionally no gradlew file present.

	r := &resolved{
		req:         Request{Builder: "host", ArtifactKind: "jar"},
		src:         Source{Path: srcDir},
		cacheKeyStr: "test-key",
	}
	err := realHostRunner{}.RunBuild(context.Background(), r, srcDir, "")
	if err == nil {
		t.Fatal("expected error when ./gradlew is missing")
	}
	if !strings.Contains(err.Error(), "gradle wrapper not present") {
		t.Errorf("error %q should explain why; got %v", err, err)
	}
}

// TestResolveBuild_HostBuilderSkipsPins is the integration-level
// guard: with Builder=host, resolveBuild succeeds even when the JDK
// version has no pinned docker image. Without this branch the host
// build would fail at the pin lookup before ever reaching the host
// runner.
func TestResolveBuild_HostBuilderSkipsPins(t *testing.T) {
	if _, err := exec.LookPath("java"); err != nil {
		t.Skip("java not on PATH; host builder resolve test needs a JVM")
	}

	srcDir := initGitRepo(t)
	req := Request{
		SourcePath:   srcDir,
		RevisionSpec: "HEAD",
		ArtifactKind: "jar",
		JDKVersion:   "99", // No such pinned image — must NOT block host builds.
		Builder:      "host",
		GradleTask:   "shadowJar",
		Platform:     "linux/amd64",
	}
	r, err := resolveBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("resolveBuild with Builder=host should NOT consult pins; got: %v", err)
	}
	if !strings.HasPrefix(r.imageRef, "host:") {
		t.Errorf("imageRef = %q; want 'host:'-prefixed", r.imageRef)
	}
	if !strings.HasPrefix(r.imageDigest, "sha256:") {
		t.Errorf("imageDigest = %q; want sha256:-prefixed", r.imageDigest)
	}
	if r.cacheKeyStr == "" {
		t.Error("cache key not materialized for host build")
	}
}
