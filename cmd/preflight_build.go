package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// preflightBuildChecks adds LOCAL-side checks for any node that
// carries a `build:` block. Distinct from the existing target-side
// preflights (those verify the deploy host; these verify the machine
// running `trond` itself, since builds always run locally and
// transfer artifacts to the target via the SSH path).
//
// Returns an empty slice when no node has a build block — the
// caller appends unconditionally so build-less intents pay no
// preflight cost.
//
// Check naming: `build-<dim>` so a `trond preflight -o json` consumer
// can group/filter on the prefix. Failures here gate apply just like
// any other preflight failure (overall=fail → ExitPreflightFailure).
func preflightBuildChecks(ctx context.Context, parsed *intent.Intent, intentPath string) []checkResult {
	var checks []checkResult

	// Aggregate: track what builders are in use and what unique
	// source dirs the build pipeline will read from. Lets us run
	// each shared check (git on PATH, docker reachable, java on
	// PATH) ONCE no matter how many nodes share the same setup.
	type buildPlan struct {
		sourceResolved string
		builder        string // "docker" | "host"
	}
	var plans []buildPlan
	for _, n := range parsed.Nodes {
		if n.Build == nil {
			continue
		}
		src := resolveBuildSourceForPreflight(n.Build.Source, intentPath)
		builder := n.Build.Builder
		if builder == "" {
			builder = "docker" // matches builder.go's withDefaults
		}
		plans = append(plans, buildPlan{sourceResolved: src, builder: builder})
	}
	if len(plans) == 0 {
		return nil
	}

	// Shared check: git is on PATH. The build pipeline shells out to
	// `git rev-parse` to resolve revisions + compute patch hashes,
	// so a missing git binary breaks every build.
	checks = append(checks, checkLocalGit())

	// Per-source: source path exists, is a directory, has a .git
	// (or is inside one). Without git metadata, source.Resolve fails
	// before the runner gets a chance.
	checked := map[string]bool{}
	for _, p := range plans {
		if checked[p.sourceResolved] {
			continue
		}
		checked[p.sourceResolved] = true
		checks = append(checks, checkBuildSource(p.sourceResolved))
	}

	// Builder-specific:
	//   docker → ensure a docker daemon is reachable locally (the
	//     builder container needs to launch here, NOT on the deploy
	//     target). This is separate from the existing target-side
	//     `checkDocker` which probes the SSH host.
	//   host → ensure java is on local PATH (for builder identity)
	//     AND each source has a ./gradlew (the runner refuses
	//     otherwise). java check is shared; gradlew is per-source.
	needLocalDocker := false
	needLocalJava := false
	for _, p := range plans {
		switch p.builder {
		case "docker":
			needLocalDocker = true
		case "host":
			needLocalJava = true
		}
	}
	if needLocalDocker {
		checks = append(checks, checkLocalDocker(ctx))
	}
	if needLocalJava {
		checks = append(checks, checkLocalJava(ctx))
	}
	if needLocalJava {
		checkedGW := map[string]bool{}
		for _, p := range plans {
			if p.builder != "host" || checkedGW[p.sourceResolved] {
				continue
			}
			checkedGW[p.sourceResolved] = true
			checks = append(checks, checkSourceGradlew(p.sourceResolved))
		}
	}
	return checks
}

// resolveBuildSourceForPreflight applies FR-021's intent-relative path
// resolution. Mirrors internal/apply/build.go's resolveBuildSource
// but doesn't error on the empty case (preflight wants to surface
// it as a check failure, not an early-return).
func resolveBuildSourceForPreflight(source, intentPath string) string {
	if source == "" {
		return ""
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source)
	}
	if intentPath != "" {
		return filepath.Clean(filepath.Join(filepath.Dir(intentPath), source))
	}
	if abs, err := filepath.Abs(source); err == nil {
		return abs
	}
	return source
}

func checkLocalGit() checkResult {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return checkResult{
			Name:    "build-git",
			Status:  "fail",
			Message: "git not found on local PATH; required to resolve revisions and compute patch hashes",
		}
	}
	return checkResult{
		Name:    "build-git",
		Status:  "pass",
		Message: strings.TrimSpace(string(out)),
	}
}

func checkBuildSource(path string) checkResult {
	name := "build-source"
	if base := filepath.Base(path); base != "" && base != "." && base != "/" {
		name = "build-source-" + base
	}
	if path == "" {
		return checkResult{
			Name:    name,
			Status:  "fail",
			Message: "build.source is empty; set the path to your java-tron checkout",
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return checkResult{
			Name:    name,
			Status:  "fail",
			Message: fmt.Sprintf("%s: %s", path, err.Error()),
		}
	}
	if !info.IsDir() {
		return checkResult{
			Name:    name,
			Status:  "fail",
			Message: fmt.Sprintf("%s is not a directory", path),
		}
	}
	// Either the source root has .git/ OR `git -C <src> rev-parse`
	// succeeds (covers submodules + working-tree checkouts). Falling
	// back to the git call keeps the check accurate for less common
	// layouts without us re-implementing git's walk logic.
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return checkResult{Name: name, Status: "pass", Message: path}
	}
	if err := exec.Command("git", "-C", path, "rev-parse", "--git-dir").Run(); err != nil {
		return checkResult{
			Name:    name,
			Status:  "fail",
			Message: fmt.Sprintf("%s is not a git repository (run `git init` or `git clone`)", path),
		}
	}
	return checkResult{Name: name, Status: "pass", Message: path}
}

func checkLocalDocker(ctx context.Context) checkResult {
	// `docker version --format` is a daemon ping — fails when the
	// CLI is present but the daemon isn't, which is the failure mode
	// we actually care about (a docker-builder run would hang on
	// `docker run`).
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return checkResult{
			Name:    "build-docker-local",
			Status:  "fail",
			Message: "docker daemon not reachable from this host; required for --builder docker",
		}
	}
	return checkResult{
		Name:    "build-docker-local",
		Status:  "pass",
		Message: "docker server " + strings.TrimSpace(string(out)),
	}
}

func checkLocalJava(ctx context.Context) checkResult {
	out, err := exec.CommandContext(ctx, "java", "-version").CombinedOutput()
	if err != nil {
		return checkResult{
			Name:    "build-host-jdk",
			Status:  "fail",
			Message: "java not on local PATH; required for --builder host",
		}
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return checkResult{
		Name:    "build-host-jdk",
		Status:  "pass",
		Message: first,
	}
}

func checkSourceGradlew(srcPath string) checkResult {
	name := "build-host-gradlew"
	if base := filepath.Base(srcPath); base != "" && base != "." && base != "/" {
		name = "build-host-gradlew-" + base
	}
	gradlewPath := filepath.Join(srcPath, "gradlew")
	info, err := os.Stat(gradlewPath)
	if err != nil {
		return checkResult{
			Name:    name,
			Status:  "fail",
			Message: fmt.Sprintf("%s not found; --builder host requires a gradle wrapper (run `gradle wrapper` in the source tree)", gradlewPath),
		}
	}
	// Executable bit check — a non-executable gradlew leads to a
	// confusing "permission denied" at build time. Surface it now.
	if info.Mode()&0o111 == 0 {
		return checkResult{
			Name:    name,
			Status:  "fail",
			Message: fmt.Sprintf("%s is not executable (chmod +x gradlew)", gradlewPath),
		}
	}
	return checkResult{Name: name, Status: "pass", Message: gradlewPath}
}
