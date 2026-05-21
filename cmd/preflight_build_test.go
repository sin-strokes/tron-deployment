package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// TestPreflightBuildChecks_NoBuildBlock is the regression guard for
// "build-less intents pay no cost": if no node has a build block,
// the build-side checks must return nil (not even an empty header
// check), so existing target-only intents see no change in their
// preflight output.
func TestPreflightBuildChecks_NoBuildBlock(t *testing.T) {
	parsed := &intent.Intent{
		Nodes: []intent.NodeSpec{{Type: "fullnode"}},
	}
	checks := preflightBuildChecks(context.Background(), parsed, "")
	if checks != nil {
		t.Errorf("build-less intent should produce 0 build checks; got %d", len(checks))
	}
}

// TestPreflightBuildChecks_HostBuilderHappyPath: a fully-valid build
// block under a real git repo with a stub gradlew produces only
// "pass" results (assuming java is on PATH). Pins the shared-vs-per-
// source separation: build-git fires once, build-source/-gradlew
// fire per unique source.
func TestPreflightBuildChecks_HostBuilderHappyPath(t *testing.T) {
	srcDir := makeGitRepoForPreflight(t)
	writeGradlewFor(t, srcDir, true)

	parsed := &intent.Intent{
		Nodes: []intent.NodeSpec{{
			Type:  "fullnode",
			Build: &intent.BuildSpec{Source: srcDir, Builder: "host"},
		}},
	}

	checks := preflightBuildChecks(context.Background(), parsed, "")

	// Required check names — exact names depend on the basename of
	// the temp dir, so match by prefix.
	requireCheck(t, checks, "build-git")
	requireCheckPrefix(t, checks, "build-source-")
	requireCheck(t, checks, "build-host-jdk")
	requireCheckPrefix(t, checks, "build-host-gradlew-")

	// No "build-docker-local" — we only need it for builder=docker.
	if has(checks, "build-docker-local") {
		t.Error("host builder should NOT trigger build-docker-local")
	}
}

// TestPreflightBuildChecks_DockerBuilderTriggersDockerCheck: pins
// the asymmetric coverage — docker builders need a local docker
// check (separate from the existing target-side checkDocker), and
// must NOT trigger build-host-jdk / build-host-gradlew which are
// expensive shell-outs that would just confuse the user.
func TestPreflightBuildChecks_DockerBuilderTriggersDockerCheck(t *testing.T) {
	srcDir := makeGitRepoForPreflight(t)

	parsed := &intent.Intent{
		Nodes: []intent.NodeSpec{{
			Type:  "fullnode",
			Build: &intent.BuildSpec{Source: srcDir, Builder: "docker"},
		}},
	}

	checks := preflightBuildChecks(context.Background(), parsed, "")
	requireCheck(t, checks, "build-docker-local")
	if has(checks, "build-host-jdk") {
		t.Error("docker builder should NOT trigger build-host-jdk")
	}
	for _, c := range checks {
		if strings.HasPrefix(c.Name, "build-host-gradlew") {
			t.Errorf("docker builder should NOT trigger %q", c.Name)
		}
	}
}

// TestCheckBuildSource pins the per-source check decisions: missing
// path fails; non-directory fails; directory without .git fails;
// directory with .git passes. The decisions feed directly into
// what the operator sees in the preflight table.
func TestCheckBuildSource(t *testing.T) {
	t.Run("missing path fails", func(t *testing.T) {
		r := checkBuildSource("/definitely/not/here")
		if r.Status != "fail" {
			t.Errorf("missing path should fail; got %s: %s", r.Status, r.Message)
		}
	})

	t.Run("regular file fails", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "not-a-dir.txt")
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := checkBuildSource(f)
		if r.Status != "fail" || !strings.Contains(r.Message, "not a directory") {
			t.Errorf("regular file should fail with not-a-directory message; got %s: %s", r.Status, r.Message)
		}
	})

	t.Run("dir without git fails", func(t *testing.T) {
		d := t.TempDir()
		r := checkBuildSource(d)
		if r.Status != "fail" || !strings.Contains(r.Message, "not a git repository") {
			t.Errorf("non-git dir should fail with git-required message; got %s: %s", r.Status, r.Message)
		}
	})

	t.Run("git repo passes", func(t *testing.T) {
		d := makeGitRepoForPreflight(t)
		r := checkBuildSource(d)
		if r.Status != "pass" {
			t.Errorf("git repo should pass; got %s: %s", r.Status, r.Message)
		}
	})
}

// TestCheckSourceGradlew: presence + executable bit.
func TestCheckSourceGradlew(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bit semantics differ on Windows; the unit logic is platform-agnostic regardless")
	}

	t.Run("missing gradlew fails", func(t *testing.T) {
		r := checkSourceGradlew(t.TempDir())
		if r.Status != "fail" {
			t.Errorf("missing gradlew should fail; got %s", r.Status)
		}
	})

	t.Run("non-executable gradlew fails with chmod hint", func(t *testing.T) {
		d := t.TempDir()
		writeGradlewFor(t, d, false)
		r := checkSourceGradlew(d)
		if r.Status != "fail" || !strings.Contains(r.Message, "chmod +x") {
			t.Errorf("non-exec gradlew should fail with chmod hint; got %s: %s", r.Status, r.Message)
		}
	})

	t.Run("executable gradlew passes", func(t *testing.T) {
		d := t.TempDir()
		writeGradlewFor(t, d, true)
		r := checkSourceGradlew(d)
		if r.Status != "pass" {
			t.Errorf("exec gradlew should pass; got %s: %s", r.Status, r.Message)
		}
	})
}

// TestResolveBuildSourceForPreflight matches FR-021 semantics: absolute
// passthrough; relative resolves against the intent file's dir;
// empty → empty (caller surfaces as fail).
func TestResolveBuildSourceForPreflight(t *testing.T) {
	t.Run("absolute passthrough", func(t *testing.T) {
		got := resolveBuildSourceForPreflight("/abs/src", "/some/intent.yaml")
		if got != "/abs/src" {
			t.Errorf("absolute should pass through; got %q", got)
		}
	})
	t.Run("relative resolves against intent dir", func(t *testing.T) {
		got := resolveBuildSourceForPreflight("../java-tron", "/path/to/intent.yaml")
		if got != "/path/java-tron" {
			t.Errorf("expected /path/java-tron; got %q", got)
		}
	})
	t.Run("empty source returns empty", func(t *testing.T) {
		if got := resolveBuildSourceForPreflight("", "/x"); got != "" {
			t.Errorf("empty source should pass through; got %q", got)
		}
	})
}

// --- test helpers ---

// makeGitRepoForPreflight creates a TempDir + a `.git/` marker so
// checkBuildSource recognizes it as a repo without us shelling out
// to actually run `git init`. The marker alone is sufficient because
// checkBuildSource short-circuits on the .git stat before trying
// rev-parse.
func makeGitRepoForPreflight(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeGradlewFor(t *testing.T, dir string, executable bool) {
	t.Helper()
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(filepath.Join(dir, "gradlew"), []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func requireCheck(t *testing.T, checks []checkResult, name string) {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return
		}
	}
	t.Errorf("missing required check %q; got names: %v", name, checkNames(checks))
}

func requireCheckPrefix(t *testing.T, checks []checkResult, prefix string) {
	t.Helper()
	for _, c := range checks {
		if strings.HasPrefix(c.Name, prefix) {
			return
		}
	}
	t.Errorf("missing required check with prefix %q; got names: %v", prefix, checkNames(checks))
}

func has(checks []checkResult, name string) bool {
	for _, c := range checks {
		if c.Name == name {
			return true
		}
	}
	return false
}

func checkNames(checks []checkResult) []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = c.Name
	}
	return out
}
