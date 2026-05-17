package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/build"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// `trond build` produces a deployable java-tron artifact (JAR in
// Phase 1; image lands in Phase 3) from a source tree, by running
// gradle inside a pinned Eclipse Temurin container.
//
// Design + rationale: specs/002-trond-build-pipeline/{spec,plan}.md.
//
// Output schema: schemas/output/build.schema.json.

var (
	buildSourcePath    string
	buildRevisionSpec  string
	buildArtifactKind  string
	buildJDKVersion    string
	buildGradleTask    string
	buildGradleArgs    []string
	buildBuilder       string
	buildImageTag      string
	buildImageOverride string
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build a java-tron artifact (JAR or image) from source",
	Long: `Build runs gradle inside a pinned Eclipse Temurin container against
the given java-tron source tree, producing either a fat JAR or a
docker image. Results are content-addressed by git revision + builder
image digest + task + args, so repeated invocations against the same
inputs return immediately.

trond ships no JDK or Gradle. The builder image is pulled on first
use and pinned via go:embed so the build is reproducible across
trond installs of the same version.

Examples:

  # Build the default fat JAR from the current branch HEAD.
  trond build --source ./java-tron --artifact jar -o json

  # Build with an explicit revision and gradle flags.
  trond build --source ./java-tron --revision v4.7.7 \
      --gradle-arg=--offline --gradle-arg=-Dversion=mytest -o json

  # Override the builder image (emergency: pinned digest unreachable).
  trond build --source ./java-tron \
      --builder-image-override eclipse-temurin:8-jdk@sha256:abcd...`,
	RunE: runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&buildSourcePath, "source", "",
		"Path to the java-tron source tree (required; relative to CWD)")
	buildCmd.Flags().StringVar(&buildRevisionSpec, "revision", "HEAD",
		"Git revision to build (HEAD, branch, tag, or sha)")
	buildCmd.Flags().StringVar(&buildArtifactKind, "artifact", "jar",
		"Artifact kind: 'jar' or 'image'")
	buildCmd.Flags().StringVar(&buildJDKVersion, "jdk", "8",
		"JDK version for the builder container (8|11|17|21)")
	buildCmd.Flags().StringVar(&buildGradleTask, "gradle-task", "",
		"Gradle task name (defaults: 'shadowJar' for jar, 'dockerBuild' for image)")
	buildCmd.Flags().StringArrayVar(&buildGradleArgs, "gradle-arg", nil,
		"Extra gradle args (repeatable; e.g. --gradle-arg=--offline). "+
			"Restricted to a flag-name allowlist; see spec FR-022.")
	buildCmd.Flags().StringVar(&buildBuilder, "builder", "docker",
		"Builder backend: 'docker' (default) or 'host' (uses local gradle)")
	buildCmd.Flags().StringVar(&buildImageTag, "tag", "",
		"Image tag to apply when --artifact=image (e.g. mytest:dev)")
	buildCmd.Flags().StringVar(&buildImageOverride, "builder-image-override", "",
		"Override the pinned builder image (escape hatch; see FR-024)")
	rootCmd.AddCommand(buildCmd)
}

// runBuild wires CLI flags into a build.Request, installs the
// signal-aware context for SIGINT propagation (FR-016), and emits
// either the success Result or a structured error envelope.
func runBuild(cmd *cobra.Command, _ []string) error {
	// FR-021: --source relative to CWD.
	resolvedSource := buildSourcePath
	if resolvedSource != "" && !filepath.IsAbs(resolvedSource) {
		abs, err := filepath.Abs(resolvedSource)
		if err == nil {
			resolvedSource = abs
		}
	}

	req := build.Request{
		SourcePath:           resolvedSource,
		RevisionSpec:         buildRevisionSpec,
		JDKVersion:           buildJDKVersion,
		ArtifactKind:         buildArtifactKind,
		GradleTask:           buildGradleTask,
		GradleArgs:           buildGradleArgs,
		Builder:              buildBuilder,
		ImageTag:             buildImageTag,
		BuilderImageOverride: buildImageOverride,
	}

	// SIGINT-aware context. Build container + git subprocesses all
	// run under this; cancellation propagates to subprocess kill.
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	outputFmt, _ := cmd.Flags().GetString("output")

	res, err := build.Run(ctx, req)
	if err != nil {
		var se *output.StructuredError
		if errors.As(err, &se) {
			output.WriteError(os.Stderr, se, outputFmt)
			os.Exit(se.ExitCode)
		}
		return err
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, res)
	}
	if res.CacheHit {
		fmt.Printf("✓ cache hit: %s (%d ms)\n", res.CacheKey, res.DurationMs)
	} else {
		fmt.Printf("✓ built: %s\n  → %s\n  sha256: %s\n  %d ms\n",
			res.CacheKey, res.ArtifactPath, res.SHA256, res.DurationMs)
	}
	return nil
}
