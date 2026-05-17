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
//	runner.go    — dockerRunner interface + real exec impl (testability)
//	builder.go   — Run() orchestrator; flow split into resolve / build / finalize
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

// resolved is the internal carrier between phases. Each helper takes
// what it needs and returns the next step's input. Keeps Run()
// readable.
type resolved struct {
	req         Request
	src         Source
	imageRef    string
	imageDigest string
	key         CacheKey
	cacheKeyStr string
}

// Run executes (or cache-hits) a build for the given request. The
// returned Result is what cmd/build.go emits as JSON; on failure a
// structured *output.StructuredError is returned with the appropriate
// error_code so the wire envelope matches the CLI/MCP contract.
//
// Lifecycle (each step is its own helper so the flow stays readable
// and individual phases are testable):
//
//  1. Validate + resolve (resolveBuild).
//  2. Cache fast path (no lock).
//  3. Acquire flock and re-check (FR-015).
//  4. Audit `in_progress` (FR-023).
//  5. Execute gradle in container (executeBuild).
//  6. Validate the produced artifact (FR-011).
//  7. Promote .tmp → final, persist manifest, audit terminal event.
//
// SIGINT propagation: ctx is honored by every subprocess. Partial
// output is cleaned up before any error return.
func Run(ctx context.Context, req Request) (*Result, error) {
	started := time.Now()

	r, err := resolveBuild(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := EnsureCacheDirs(); err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"ensure cache dirs: %s", err.Error())
	}

	// Fast path: cheap stat, no lock.
	if hit, _ := Lookup(r.key); hit != nil && hit.Hit {
		return resultFromManifest(hit.Manifest, true, time.Since(started).Milliseconds()), nil
	}

	// Serialize same-key concurrent builds (FR-015).
	release, lockErr := AcquireCacheLock(CacheDir(), r.cacheKeyStr)
	if lockErr != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"acquire build lock: %s", lockErr.Error())
	}
	defer release()

	// Re-check after lock — winner of the race may have finished.
	if hit, _ := Lookup(r.key); hit != nil && hit.Hit {
		return resultFromManifest(hit.Manifest, true, time.Since(started).Milliseconds()), nil
	}

	_ = AppendAuditEvent(PhaseInProgress, r.cacheKeyStr, "", started)

	if r.req.ArtifactKind != "jar" {
		_ = AppendAuditEvent(PhaseFailed, r.cacheKeyStr, "NOT_IMPLEMENTED", started)
		return nil, output.NewErrorf("NOT_IMPLEMENTED", output.ExitGeneralError,
			"artifact=%s is not yet supported by Phase 1 (jar only)", r.req.ArtifactKind)
	}

	manifest, err := buildJAR(ctx, r, started)
	if err != nil {
		// Audit + propagate. The buildJAR helper has already done
		// best-effort cleanup of .tmp output.
		var se *output.StructuredError
		if errors.As(err, &se) {
			_ = AppendAuditEvent(phaseFromError(ctx, se.Code), r.cacheKeyStr, se.Code, started)
			return nil, se
		}
		_ = AppendAuditEvent(PhaseFailed, r.cacheKeyStr, "BUILD_FAILED", started)
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"gradle build failed: %s", err.Error())
	}

	_ = AppendAuditEvent(PhaseSuccess, r.cacheKeyStr, "", started)
	return resultFromManifest(manifest, false, manifest.DurationMs), nil
}

// resolveBuild handles steps 1-2 from the lifecycle: defaults +
// validation, builder image pin resolution, git revision resolution,
// cache key materialization.
func resolveBuild(ctx context.Context, req Request) (*resolved, error) {
	req = req.withDefaults()
	if err := req.validate(); err != nil {
		return nil, err
	}

	imageRef, imageDigest, ok := pins.Resolve(req.JDKVersion, req.BuilderImageOverride)
	if !ok {
		return nil, output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"no pinned builder image for JDK version %q (available: %v)",
			req.JDKVersion, pins.Versions()).
			WithSuggestions(
				"Use one of "+strings.Join(pins.Versions(), ", "),
				"Or pass --builder-image-override <ref@sha256:...>",
			)
	}

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
	return &resolved{
		req:         req,
		src:         src,
		imageRef:    imageRef,
		imageDigest: imageDigest,
		key:         key,
		cacheKeyStr: key.String(),
	}, nil
}

// buildJAR runs gradle for artifact_kind=jar, validates the produced
// JAR (FR-011), promotes it to the final name, and persists the
// manifest. Best-effort cleanup of partial output on any error path.
func buildJAR(ctx context.Context, r *resolved, started time.Time) (*Manifest, error) {
	outDir := filepath.Join(CacheDir(), "out")
	outFinal := filepath.Join(outDir, r.cacheKeyStr+".jar")
	outTmp := outFinal + ".tmp"

	_ = os.Remove(outTmp) // stale .tmp from a prior cancelled run

	runErr := defaultRunner.RunDockerBuild(ctx, r, outDir, outTmp)
	if runErr != nil {
		_ = os.Remove(outTmp)
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, output.NewErrorf("BUILD_CANCELLED", 130,
				"build cancelled by user").
				WithSuggestions("Re-run when ready; cached partial output has been cleaned")
		}
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"gradle build failed: %s", runErr.Error()).
			WithSuggestions(
				"Inspect the gradle output above for compile errors",
				"Verify the source tree is a clean java-tron checkout",
			)
	}

	const fullNodeMain = "org.tron.program.FullNode"
	if err := ValidateJARMainClass(outTmp, fullNodeMain); err != nil {
		_ = os.Remove(outTmp)
		return nil, output.NewErrorf("INVALID_ARTIFACT", output.ExitGeneralError,
			"produced JAR is not a java-tron node: %s", err.Error()).
			WithSuggestions(
				fmt.Sprintf("Verify the gradle task '%s' is the shadow-jar target for FullNode", r.req.GradleTask),
				"Override with --gradle-task if the source uses a different task name",
			)
	}

	if err := os.Rename(outTmp, outFinal); err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"finalize artifact: %s", err.Error())
	}

	sum, err := fileSHA256(outFinal)
	if err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"hash artifact: %s", err.Error())
	}

	manifest := &Manifest{
		CacheKey:           r.cacheKeyStr,
		SourcePath:         r.src.Path,
		SourceRevision:     r.src.ResolvedRevision,
		PatchHash:          r.src.PatchHash,
		Dirty:              r.src.DirtyState,
		BuilderImage:       r.imageRef,
		BuilderImageDigest: r.imageDigest,
		JDKVersion:         r.req.JDKVersion,
		ArtifactKind:       "jar",
		ArtifactPath:       outFinal,
		SHA256:             sum,
		GradleTask:         r.req.GradleTask,
		GradleArgs:         r.req.GradleArgs,
		Builder:            r.req.Builder,
		DurationMs:         time.Since(started).Milliseconds(),
		CreatedAt:          time.Now().UTC(),
	}
	if err := Save(manifest); err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"persist manifest: %s", err.Error())
	}
	return manifest, nil
}

// phaseFromError maps a structured error code to the right audit
// phase. Cancellation is distinct from generic failure.
func phaseFromError(ctx context.Context, code string) AuditPhase {
	if code == "BUILD_CANCELLED" || errors.Is(ctx.Err(), context.Canceled) {
		return PhaseCancelled
	}
	return PhaseFailed
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

// allowedEnvPassthrough collects env vars to forward into the build
// container. Two sources:
//
//  1. trond's invocation environment, filtered by the FR-019
//     allowlist (so the developer's `GRADLE_OPTS=-Xmx4g` reaches
//     gradle even when not declared in intent).
//  2. The intent's `build.env: { KEY: VALUE }` map, also allowlisted.
//
// Intent values override host values on key collision (last writer
// wins in docker's `-e`). Output is sorted for reproducible argv.
func allowedEnvPassthrough(intent map[string]string) []string {
	out := []string{}
	for k := range envAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, orgGradleProjectPrefix) {
			continue
		}
		out = append(out, e)
	}
	keys := make([]string, 0, len(intent))
	for k := range intent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := ValidateEnvKey(k); err != nil {
			continue
		}
		out = append(out, k+"="+intent[k])
	}
	sort.Strings(out)
	return out
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
