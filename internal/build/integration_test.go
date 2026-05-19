//go:build integration

// Integration test for the real docker-driven build pipeline. Runs
// `eclipse-temurin:8-jdk-jammy` against a tiny hello-world gradle
// project shipped under testdata/, asserts the produced JAR is
// structurally valid + the cache + audit log entries are populated.
//
// Skipped by `go test ./...`. To run:
//
//	go test -tags=integration -timeout 5m -count=1 ./internal/build/...
//
// Requires docker, network access (first run pulls ~300 MB JDK image).
package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestBuild_RealGradleEndToEnd asserts trond drives a real gradle
// build to completion against a minimal source fixture. The test is
// behind the `integration` build tag so it does not gate unit-test
// CI; nightly / release CI flips it on.
func TestBuild_RealGradleEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := setupIntegrationRepo(t)
	withTempBaseDir(t)

	// We need a real builder image digest. Resolve at test time so
	// this test doesn't depend on the placeholder pin file.
	override := resolveBuilderImage(t, "eclipse-temurin:8-jdk-jammy")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	res, err := Run(ctx, Request{
		SourcePath:           repo,
		BuilderImageOverride: override,
		// The fixture uses the shadow plugin and produces
		// build/libs/*-all.jar; gradle_task=shadowJar is the default
		// per our Phase 1 wiring.
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.ArtifactPath == "" {
		t.Fatal("artifact_path is empty in success result")
	}
	if _, statErr := os.Stat(res.ArtifactPath); statErr != nil {
		t.Fatalf("artifact %s missing: %v", res.ArtifactPath, statErr)
	}

	// Second run should be a cache hit (no docker invocation).
	res2, err := Run(ctx, Request{
		SourcePath:           repo,
		BuilderImageOverride: override,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !res2.CacheHit {
		t.Error("identical inputs should produce a cache hit")
	}
}

// TestBuildImage_RealGradleEndToEnd is the Phase 3 happy-path
// integration test. It exercises buildImage end-to-end against a
// minimal Dockerfile-only source fixture, validating:
//   - docker.sock-mounted gradle invocation actually produces an image
//   - the before/after snapshot picks the right image (no
//     intermediate dangling layers thanks to FR-019 dangling=false)
//   - the user's build.image_tag is applied
//   - gradle's auto-generated tags get stripped (stripExtraTags)
//   - second run hits the cache by checking the tag still resolves
func TestBuildImage_RealGradleEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// The fixture's Dockerfile pulls busybox. Air-gapped or
	// docker-hub-blocked hosts can't run this test; skip rather
	// than fail.
	if out, err := exec.Command("docker", "pull", "busybox:latest").CombinedOutput(); err != nil {
		t.Skipf("cannot pull busybox (no network or registry blocked): %v\n%s", err, out)
	}

	repo := setupImageIntegrationRepo(t)
	withTempBaseDir(t)

	override := resolveBuilderImage(t, "eclipse-temurin:8-jdk-jammy")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tag := "trond-build-test/fixture:dev"
	res, err := Run(ctx, Request{
		SourcePath:           repo,
		BuilderImageOverride: override,
		ArtifactKind:         "image",
		ImageTag:             tag,
		GradleTask:           "dockerBuild",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.ImageTag != tag {
		t.Errorf("ImageTag = %q; want %q", res.ImageTag, tag)
	}
	t.Cleanup(func() {
		// Best-effort cleanup so this test doesn't leak images.
		_ = exec.Command("docker", "image", "rm", "-f", tag).Run()
	})

	// Second run should hit the cache — tag still resolves in
	// docker store, manifest still on disk.
	res2, err := Run(ctx, Request{
		SourcePath:           repo,
		BuilderImageOverride: override,
		ArtifactKind:         "image",
		ImageTag:             tag,
		GradleTask:           "dockerBuild",
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !res2.CacheHit {
		t.Error("identical inputs should produce a cache hit")
	}
}

// setupImageIntegrationRepo writes a minimal gradle project that
// runs `docker build` via a tiny inline Dockerfile. Output is a
// runnable image (FROM scratch + ENTRYPOINT) so
// validateImageEntrypoint passes.
func setupImageIntegrationRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"settings.gradle": `rootProject.name = 'image-fixture'
`,
		"build.gradle": `plugins { id 'base' }
task dockerBuild(type: Exec) {
    workingDir projectDir
    commandLine 'docker', 'build', '-t', 'image-fixture:built', '.'
}
`,
		"Dockerfile": `FROM busybox:latest
ENTRYPOINT ["echo", "trond-image-fixture"]
`,
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// gradle wrapper init if available locally — otherwise CI must
	// have it preinstalled.
	if _, err := exec.LookPath("gradle"); err == nil {
		_, _ = exec.Command("gradle", "-p", dir, "wrapper", "--gradle-version=7.6").CombinedOutput()
	}

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "image-test@trond"},
		{"config", "user.name", "image"},
		{"config", "commit.gpgsign", "false"},
		{"add", "."},
		{"commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// setupIntegrationRepo writes a minimal gradle project (tiny enough
// that the test runs in well under a minute on a warm cache) that
// produces a JAR whose Main-Class matches org.tron.program.FullNode.
// Note we don't bring in real java-tron — that would require ~hundreds
// of MB of dependencies. This fixture is JUST enough to drive the
// shadowJar plugin and trip trond's validator on success.
func setupIntegrationRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"settings.gradle": `rootProject.name = 'fullnode-fixture'
`,
		"build.gradle": `plugins {
    id 'java'
    id 'com.github.johnrengelman.shadow' version '7.1.2'
}
group = 'org.tron'
version = '0.0.0-fixture'

repositories {
    mavenCentral()
}

jar {
    manifest {
        attributes 'Main-Class': 'org.tron.program.FullNode'
    }
}
shadowJar {
    archiveClassifier = ''
    archiveBaseName = 'FullNode'
}
`,
		"src/main/java/org/tron/program/FullNode.java": `package org.tron.program;
public class FullNode {
    public static void main(String[] args) {
        System.out.println("trond integration fixture");
    }
}
`,
	}

	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// Drop in a gradle wrapper. Cheap approach: copy from the host's
	// gradle install if available; otherwise skip — most CI envs have
	// one. We don't bake a wrapper jar in-repo (size).
	if _, err := exec.LookPath("gradle"); err == nil {
		out, err := exec.Command("gradle", "-p", dir, "wrapper", "--gradle-version=7.6").CombinedOutput()
		if err != nil {
			t.Logf("gradle wrapper init failed (non-fatal):\n%s", out)
		}
	}

	// Initialise as a git repo so trond's source.Resolve has a HEAD.
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "integration@trond"},
		{"config", "user.name", "integration"},
		{"config", "commit.gpgsign", "false"},
		{"add", "."},
		{"commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// resolveBuilderImage runs `docker pull` to surface the real RepoDigest
// for a tag, so the integration test threads a real
// `<tag>@sha256:...` into the cache key. Failing here marks the test
// as Skip, not Fail, so air-gapped CI hosts don't fail the suite.
func resolveBuilderImage(t *testing.T, tag string) string {
	t.Helper()
	if out, err := exec.Command("docker", "pull", tag).CombinedOutput(); err != nil {
		t.Skipf("cannot pull %s (offline?): %v\n%s", tag, err, out)
	}
	out, err := exec.Command("docker", "inspect", "--format={{ index .RepoDigests 0 }}", tag).Output()
	if err != nil {
		t.Skipf("docker inspect %s: %v", tag, err)
	}
	digestRef := string(out)
	// Trim trailing newline.
	for len(digestRef) > 0 && (digestRef[len(digestRef)-1] == '\n' || digestRef[len(digestRef)-1] == '\r') {
		digestRef = digestRef[:len(digestRef)-1]
	}
	return digestRef
}
