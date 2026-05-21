package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// dockerRunner abstracts the "run a docker command" step so tests can
// substitute a recorder/mock without spinning up a real Docker
// daemon. Production wiring is the exec-based realDockerRunner.
//
// The interface intentionally accepts the full argv (not pieces) —
// tests assert on that argv to enforce FR-022's argv-only invocation
// contract (no `bash -c "...interpolated..."`).
type dockerRunner interface {
	RunDockerBuild(ctx context.Context, r *resolved, outDir, outTmp string) error
}

// defaultRunner is package-level so tests can swap it via
// `t.Cleanup(func() { defaultRunner = orig })`. Production uses
// realDockerRunner which shells out to the docker CLI.
var defaultRunner dockerRunner = realDockerRunner{}

// dockerBuildScript is the only piece of shell trond runs and it's
// a compile-time constant. User input (gradle_task, gradle_args)
// arrives through `"$@"` argv expansion AFTER `--`; output filename
// arrives through an env var. Both channels are shell-quote-safe
// independent of their contents. FR-022 holds: no path from user
// input to shell metacharacter interpretation.
//
// Why bash and not argv? Because the build produces files inside the
// container's /src tree (gradle writes to build/libs/*.jar) and we
// need to *move* them into /out so they survive container teardown.
// A `./gradlew` argv invocation alone leaves the artifact in the
// container's ephemeral layer.
//
// `ls -S` sorts by size, so `head -n1` picks the largest jar — for
// the shadow plugin that's the fat jar with dependencies; thin jars
// (if also emitted) are smaller. ValidateJARMainClass rejects any
// non-FullNode jar that wins this heuristic.
const dockerBuildScript = `set -e
cd /src
./gradlew "$@"
JAR=$(ls -S build/libs/*.jar 2>/dev/null | head -n1)
if [ -z "$JAR" ]; then
  echo "trond: gradle produced no .jar in build/libs/" >&2
  exit 64
fi
cp "$JAR" "/out/$OUT_NAME"
`

type realDockerRunner struct{}

func (realDockerRunner) RunDockerBuild(ctx context.Context, r *resolved, outDir, outTmp string) error {
	if r.req.Builder == "host" {
		return fmt.Errorf("--builder host not implemented in Phase 1 (use docker)")
	}

	gradleCache := filepath.Join(CacheDir(), "gradle")

	args := []string{
		"run", "--rm",
		// /src must be RW because gradle writes build/, .gradle/ into
		// the project tree (same as running ./gradlew on the host).
		// The user already gives gradle this access locally.
		"-v", r.src.Path + ":/src:rw",
		"-v", gradleCache + ":/root/.gradle",
		"-v", outDir + ":/out:rw",
		"--workdir", "/src",
		// Output filename passed through env, not interpolated into
		// the script (FR-022 defense in depth).
		"-e", "OUT_NAME=" + filepath.Base(outTmp),
	}
	// --platform routes to the matching variant of the multi-arch
	// builder image. On a cross-arch combination docker uses QEMU
	// emulation (binfmt-misc); 3-5× slower but functional. Required
	// for the java-tron compat matrix: amd64+JDK8 vs arm64+JDK17.
	if r.req.Platform != "" {
		args = append(args, "--platform", r.req.Platform)
	}
	for _, e := range allowedEnvPassthrough(r.req.Env) {
		args = append(args, "-e", e)
	}
	args = append(args, r.imageRef, "bash", "-c", dockerBuildScript, "--")
	args = append(args, r.req.GradleTask)
	args = append(args, r.req.GradleArgs...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	// In `-o json` mode the caller redirects stdout to a JSON buffer;
	// gradle's chatter belongs on stderr regardless of trond's output
	// mode so it never corrupts the JSON envelope.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
