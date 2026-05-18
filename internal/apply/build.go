package apply

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tronprotocol/tron-deployment/internal/build"
	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// resolveBuild handles the optional `build:` block on a node. When
// present it invokes the build pipeline (cache-hit-fast-path) and
// returns the resolved artifact plus a summary slice for the apply
// result envelope. Returns (nil, "", "", nil) when the node has no
// build block — the caller proceeds with the legacy image / jar
// source paths.
//
// Per spec/002 FR-021, a relative `build.source` resolves against
// the intent file's directory (matches docker-compose's
// `build.context` convention). The CLI's `--source` already resolves
// against CWD before constructing intent.Build, so this function
// only needs to handle the intent path.
func resolveBuild(
	ctx context.Context,
	opts Options,
	node *intent.NodeSpec,
) (summary *BuildSummary, builtJarPath, builtImageTag string, err error) {
	if node.Build == nil {
		return nil, "", "", nil
	}
	bs := node.Build

	source, srcErr := resolveBuildSource(bs.Source, opts.IntentPath)
	if srcErr != nil {
		return nil, "", "", output.NewErrorf("INVALID_SOURCE",
			output.ExitValidationError, "%s", srcErr.Error())
	}

	req := build.Request{
		SourcePath:           source,
		RevisionSpec:         bs.Revision,
		JDKVersion:           bs.JDK,
		ArtifactKind:         bs.Artifact,
		ImageTag:             bs.ImageTag,
		GradleTask:           bs.GradleTask,
		GradleArgs:           append([]string(nil), bs.GradleArgs...),
		Builder:              bs.Builder,
		BuilderImageOverride: bs.BuilderImageOverride,
		Env:                  bs.Env,
		Platform:             bs.Platform,
	}

	res, runErr := build.Run(ctx, req)
	if runErr != nil {
		// build.Run returns *output.StructuredError on user-facing
		// failure paths; propagate that directly so the wire envelope
		// is preserved.
		return nil, "", "", runErr
	}

	summary = &BuildSummary{
		CacheKey:       res.CacheKey,
		SourceRevision: res.SourceRevision,
		Dirty:          res.Dirty,
		ArtifactPath:   res.ArtifactPath,
		ImageTag:       res.ImageTag,
		SHA256:         res.SHA256,
		BuilderImage:   res.BuilderImage,
		CacheHit:       res.CacheHit,
		DurationMs:     res.DurationMs,
	}

	switch res.ArtifactKind {
	case "jar":
		builtJarPath = res.ArtifactPath
	case "image":
		// Phase 3 wires this into the docker runtime path. Phase 2
		// leaves the field populated so apply can record it but the
		// runtime switch ignores it for now.
		builtImageTag = res.ImageTag
	default:
		return nil, "", "", output.NewErrorf("INTERNAL_ERROR",
			output.ExitGeneralError,
			"build returned unknown artifact_kind %q", res.ArtifactKind)
	}
	return summary, builtJarPath, builtImageTag, nil
}

// resolveBuildSource implements FR-021's intent-relative path
// resolution. Absolute paths pass through unchanged. Relative paths
// resolve against the intent file's parent directory. If the intent
// was supplied via stdin or an in-memory source (IntentPath empty),
// the relative path is treated as relative to CWD as a fallback.
func resolveBuildSource(source, intentPath string) (string, error) {
	if source == "" {
		return "", fmt.Errorf("build.source is required")
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source), nil
	}
	if intentPath != "" {
		base := filepath.Dir(intentPath)
		return filepath.Clean(filepath.Join(base, source)), nil
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve relative source %q: %w", source, err)
	}
	return abs, nil
}
