// Package build orchestrates containerized Gradle invocations so
// developers can iterate on java-tron source and redeploy with one
// `trond apply`. trond ships no JDK / Gradle / Java compiler; the
// build environment is a pinned Eclipse Temurin container and trond
// is the conductor (spec/002, FR-022 argv-only).
//
// The package is organized as:
//
//	pins/        — go:embed builder image digest pins (FR-024)
//	lock_*.go    — flock-based serialization, posix + windows split (FR-015)
//	imagetag.go  — Docker reference validation for build.image_tag (FR-005)
//	validate.go  — gradle task/args allowlist + JAR Main-Class check (FR-022, FR-011)
//	source.go    — git shell-out: rev-parse, dirty detection (FR-002)
//	key.go       — content-addressed CacheKey naming (FR-002)
//	manifest.go  — per-build JSON manifest (FR-004)
//	cache.go     — lookup / save / artifact stat (FR-020)
//	audit.go     — two-phase build event lifecycle (FR-023)
//	builder.go   — Builder interface + docker + host impls
//
// The exported surface is intentionally small — cmd/build.go calls
// `Run`, apply integration calls `Run`, MCP calls `Run`. Everything
// else is package-internal.
package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/build/pins"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

// Request captures everything Run needs to decide a build. It is the
// pre-flight-ready, fully validated form — cobra layers and the apply
// pipeline both normalize to this struct.
type Request struct {
	SourcePath           string
	RevisionSpec         string // "HEAD" | branch | tag | sha
	JDKVersion           string // "8" | "11" | "17" | "21"
	ArtifactKind         string // "jar" | "image"
	GradleTask           string // overrides default per artifact
	GradleArgs           []string
	Builder              string // "docker" | "host"
	ImageTag             string // for artifact=image
	BuilderImageOverride string // FR-024 escape hatch
	Env                  map[string]string
}

// Result is the JSON-serializable success payload. Mirrors
// schemas/output/build.schema.json.
type Result struct {
	CacheKey       string    `json:"cache_key"`
	SourceRevision string    `json:"source_revision"`
	Dirty          bool      `json:"dirty"`
	ArtifactKind   string    `json:"artifact_kind"`
	ArtifactPath   string    `json:"artifact_path,omitempty"`
	ImageTag       string    `json:"image_tag,omitempty"`
	SHA256         string    `json:"sha256,omitempty"`
	BuilderImage   string    `json:"builder_image"`
	JDKVersion     string    `json:"jdk_version"`
	GradleTask     string    `json:"gradle_task"`
	Builder        string    `json:"builder"`
	CacheHit       bool      `json:"cache_hit"`
	DurationMs     int64     `json:"duration_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

// Run executes (or cache-hits) a build for the given request. The
// returned Result is what cmd/build.go emits as JSON; on failure a
// structured *output.StructuredError is returned with the appropriate
// error_code so the wire envelope matches the CLI/MCP contract.
//
// Lifecycle:
//
//  1. Validate inputs (gradle task / args / image_tag / env / pin
//     resolution).
//  2. Resolve git revision + dirty patch.
//  3. Build the cache key. Stat the cache — hit returns immediately.
//  4. Acquire the per-key flock (FR-015).
//  5. Re-check cache (another caller might have built while we waited).
//  6. Append `in_progress` audit event (FR-023).
//  7. Run gradle inside the container (FR-022 argv-only).
//  8. Verify produced artifact (FR-011).
//  9. Persist manifest, emit terminal audit event.
//
// SIGINT propagation: ctx is honored by every subprocess. The cleanup
// defer kills any in-flight container and removes partial output.
func Run(ctx context.Context, req Request) (*Result, error) {
	started := time.Now()

	req = req.withDefaults()
	if err := req.validate(); err != nil {
		return nil, err
	}

	// Resolve pinned builder image (FR-024 + cache key).
	imageRef, imageDigest, ok := pins.Resolve(req.JDKVersion, req.BuilderImageOverride)
	if !ok {
		return nil, output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"no pinned builder image for JDK version %q (available: %v)",
			req.JDKVersion, pins.Versions()).
			WithSuggestions(
				"Use one of " + strings.Join(pins.Versions(), ", "),
				"Or pass --builder-image-override <ref@sha256:...>",
			)
	}

	// Resolve source revision.
	src := Source{Path: req.SourcePath, RevisionSpec: req.RevisionSpec}
	if err := src.Resolve(ctx); err != nil {
		return nil, output.NewErrorf("INVALID_SOURCE", output.ExitValidationError,
			"resolve source: %s", err.Error()).
			WithSuggestions(
				"Ensure the path points at a git repository",
				"Pass --revision <sha> explicitly if the working tree isn't a git checkout",
			)
	}

	key := CacheKey{
		GitRevision:        src.ResolvedRevision,
		PatchHash:          src.PatchHash,
		BuilderImageDigest: imageDigest,
		JDKVersion:         req.JDKVersion,
		ArtifactKind:       req.ArtifactKind,
		GradleTask:         req.GradleTask,
		GradleArgs:         append([]string(nil), req.GradleArgs...),
	}
	cacheKeyStr := key.String()

	if err := EnsureCacheDirs(); err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"ensure cache dirs: %s", err.Error())
	}

	// Fast path: check before locking. Cheap.
	if hit, _ := Lookup(key); hit != nil && hit.Hit {
		return resultFromManifest(hit.Manifest, true, time.Since(started).Milliseconds()), nil
	}

	// Serialize same-key concurrent builds (FR-015).
	release, lockErr := AcquireCacheLock(CacheDir(), cacheKeyStr)
	if lockErr != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"acquire build lock: %s", lockErr.Error())
	}
	defer release()

	// Re-check after lock acquisition — winner of the race may have
	// finished while we were waiting.
	if hit, _ := Lookup(key); hit != nil && hit.Hit {
		return resultFromManifest(hit.Manifest, true, time.Since(started).Milliseconds()), nil
	}

	// Audit `in_progress` so a crash mid-build leaves a trail (FR-023).
	_ = AppendAuditEvent(PhaseInProgress, cacheKeyStr, "", started)

	if req.ArtifactKind != "jar" {
		// Phase 3 owns the image path. Surface a clear error in Phase 1.
		_ = AppendAuditEvent(PhaseFailed, cacheKeyStr, "NOT_IMPLEMENTED", started)
		return nil, output.NewErrorf("NOT_IMPLEMENTED", output.ExitGeneralError,
			"artifact=%s is not yet supported by Phase 1 (jar only)", req.ArtifactKind)
	}

	outDir := filepath.Join(CacheDir(), "out")
	outFinal := filepath.Join(outDir, cacheKeyStr+".jar")
	outTmp := outFinal + ".tmp"

	// Best-effort cleanup of stale .tmp from a prior cancelled run.
	_ = os.Remove(outTmp)

	res, runErr := runDockerBuild(ctx, req, imageRef, outDir, outTmp)
	if runErr != nil {
		_ = os.Remove(outTmp)
		if errors.Is(ctx.Err(), context.Canceled) {
			_ = AppendAuditEvent(PhaseCancelled, cacheKeyStr, "BUILD_CANCELLED", started)
			return nil, output.NewErrorf("BUILD_CANCELLED", 130,
				"build cancelled by user").
				WithSuggestions("Re-run when ready; cached partial output has been cleaned")
		}
		_ = AppendAuditEvent(PhaseFailed, cacheKeyStr, "BUILD_FAILED", started)
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"gradle build failed: %s", runErr.Error()).
			WithSuggestions(
				"Inspect the gradle output above for compile errors",
				"Verify the source tree is a clean java-tron checkout",
			)
	}

	// Validate the produced JAR before declaring success (FR-011).
	const fullNodeMain = "org.tron.program.FullNode"
	if err := ValidateJARMainClass(outTmp, fullNodeMain); err != nil {
		_ = os.Remove(outTmp)
		_ = AppendAuditEvent(PhaseFailed, cacheKeyStr, "INVALID_ARTIFACT", started)
		return nil, output.NewErrorf("INVALID_ARTIFACT", output.ExitGeneralError,
			"produced JAR is not a java-tron node: %s", err.Error()).
			WithSuggestions(
				fmt.Sprintf("Verify the gradle task '%s' is the shadow-jar target for FullNode", req.GradleTask),
				"Override with --gradle-task if the source uses a different task name",
			)
	}

	// Atomic move from .tmp to final. Cache hit checks always see a
	// complete file or no file.
	if err := os.Rename(outTmp, outFinal); err != nil {
		_ = AppendAuditEvent(PhaseFailed, cacheKeyStr, "INTERNAL_ERROR", started)
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"finalize artifact: %s", err.Error())
	}

	manifest := &Manifest{
		CacheKey:           cacheKeyStr,
		SourcePath:         src.Path,
		SourceRevision:     src.ResolvedRevision,
		PatchHash:          src.PatchHash,
		Dirty:              src.DirtyState,
		BuilderImage:       imageRef,
		BuilderImageDigest: imageDigest,
		JDKVersion:         req.JDKVersion,
		ArtifactKind:       "jar",
		ArtifactPath:       outFinal,
		SHA256:             res.SHA256,
		GradleTask:         req.GradleTask,
		GradleArgs:         req.GradleArgs,
		Builder:            req.Builder,
		DurationMs:         time.Since(started).Milliseconds(),
		CreatedAt:          time.Now().UTC(),
	}
	if err := Save(manifest); err != nil {
		_ = AppendAuditEvent(PhaseFailed, cacheKeyStr, "INTERNAL_ERROR", started)
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"persist manifest: %s", err.Error())
	}
	_ = AppendAuditEvent(PhaseSuccess, cacheKeyStr, "", started)

	return resultFromManifest(manifest, false, manifest.DurationMs), nil
}

// dockerRunResult is the small contract between Run and the
// container driver. Kept here (not in builder.go's public surface) so
// future builders (host gradle, ssh build server) can return the same
// shape.
type dockerRunResult struct {
	SHA256 string
}

// runDockerBuild does the actual `docker run eclipse-temurin ...`
// invocation. Argv-only — no `bash -c` — per FR-022 to keep
// gradle_task / gradle_args injection-safe.
func runDockerBuild(ctx context.Context, req Request, imageRef, outDir, outTmp string) (*dockerRunResult, error) {
	if req.Builder == "host" {
		// Phase 1: host builder is hooked in skeleton form so cmd
		// flag parsing works, but the body is deferred to Phase 5
		// since the docker path is enough to validate the contract.
		return nil, fmt.Errorf("--builder host not implemented in Phase 1 (use docker)")
	}

	// Prepare env passthrough. ONLY allowlisted keys (FR-019).
	envArgs := allowedEnvPassthrough(req.Env)

	// Mount the source read-only and an output dir read-write. Gradle
	// cache mounted separately so trond's container caches are
	// isolated from the user's host ~/.gradle.
	gradleCache := filepath.Join(CacheDir(), "gradle")

	args := []string{
		"run", "--rm",
		"-v", req.SourcePath + ":/src:ro",
		"-v", gradleCache + ":/root/.gradle",
		"-v", outDir + ":/out:rw",
		"--workdir", "/src",
	}
	for _, e := range envArgs {
		args = append(args, "-e", e)
	}
	args = append(args, imageRef, "./gradlew", req.GradleTask)
	args = append(args, req.GradleArgs...)
	// Tell gradle to drop the jar into our mounted /out under the
	// expected name. Argv-form: no shell interpretation of the path.
	args = append(args, "-PoutputJarPath=/out/"+filepath.Base(outTmp))

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stderr // surface progress to the user; stdout is reserved for JSON
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	// Compute sha256 of the produced jar.
	sum, err := fileSHA256(outTmp)
	if err != nil {
		return nil, fmt.Errorf("hash artifact: %w", err)
	}
	return &dockerRunResult{SHA256: sum}, nil
}

func allowedEnvPassthrough(intent map[string]string) []string {
	// Forward host env (allowlist subset) first; intent.env overrides.
	out := []string{}
	for k := range envAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	// ORG_GRADLE_PROJECT_* prefix from host.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, orgGradleProjectPrefix) {
			continue
		}
		out = append(out, e)
	}
	// Intent-provided env (already validated by ValidateEnvKey at
	// parse time; defense in depth, re-check here).
	keys := make([]string, 0, len(intent))
	for k := range intent {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic order for reproducibility
	for _, k := range keys {
		if err := ValidateEnvKey(k); err != nil {
			continue
		}
		out = append(out, k+"="+intent[k])
	}
	return out
}

func (r Request) withDefaults() Request {
	if r.JDKVersion == "" {
		r.JDKVersion = "8"
	}
	if r.ArtifactKind == "" {
		r.ArtifactKind = "jar"
	}
	if r.GradleTask == "" {
		switch r.ArtifactKind {
		case "jar":
			r.GradleTask = "shadowJar"
		case "image":
			r.GradleTask = "dockerBuild"
		}
	}
	if r.Builder == "" {
		r.Builder = "docker"
	}
	if r.RevisionSpec == "" {
		r.RevisionSpec = "HEAD"
	}
	return r
}

func (r Request) validate() error {
	if r.SourcePath == "" {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"--source is required")
	}
	if r.ArtifactKind != "jar" && r.ArtifactKind != "image" {
		return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"--artifact must be 'jar' or 'image' (got %q)", r.ArtifactKind)
	}
	if r.Builder != "docker" && r.Builder != "host" {
		return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"--builder must be 'docker' or 'host' (got %q)", r.Builder)
	}
	if err := ValidateGradleTask(r.GradleTask); err != nil {
		return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"%s", err.Error())
	}
	if err := ValidateGradleArgs(r.GradleArgs); err != nil {
		return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"%s", err.Error())
	}
	if r.ArtifactKind == "image" {
		if err := ValidateImageTag(r.ImageTag); err != nil {
			return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
				"%s", err.Error())
		}
	}
	for k := range r.Env {
		if err := ValidateEnvKey(k); err != nil {
			return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
				"%s", err.Error())
		}
	}
	return nil
}

func resultFromManifest(m *Manifest, hit bool, duration int64) *Result {
	return &Result{
		CacheKey:       m.CacheKey,
		SourceRevision: m.SourceRevision,
		Dirty:          m.Dirty,
		ArtifactKind:   m.ArtifactKind,
		ArtifactPath:   m.ArtifactPath,
		ImageTag:       m.ImageTag,
		SHA256:         m.SHA256,
		BuilderImage:   m.BuilderImage,
		JDKVersion:     m.JDKVersion,
		GradleTask:     m.GradleTask,
		Builder:        m.Builder,
		CacheHit:       hit,
		DurationMs:     duration,
		CreatedAt:      m.CreatedAt,
	}
}
