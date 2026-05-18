// Package apply runs the deploy phase of `trond apply` as a pure
// function so callers other than cobra (MCP server, recipe runner,
// e2e test harness) can drive it without forking a subprocess.
//
// Scope: this package owns steps 8-10 of the apply pipeline as
// originally documented in cmd/apply.go::runApply:
//
//  8. Render HOCON + compose / systemd, derive JVM args
//  9. Hand off to docker / jar runtime, persist node into state
//  10. Optionally block on the node's HTTP API readiness (--wait)
//
// What it does NOT own — these stay in cmd/apply.go because they're
// either presentation-layer or require interactive context the
// callers control:
//
//   - Parsing flags / loading the intent file (caller responsibility)
//   - Resolving target.Target (cmd needs SSH cert handling, MCP gets
//     its target from a fresh resolveTarget call)
//   - Acquiring the state lock (cmd's defer pattern; MCP holds it
//     for the duration of the tool call)
//   - The HUMAN_REQUIRED gate for changed intents (this is a
//     human-confirmation policy; callers decide when it fires)
//   - Audit log writes (cmd-only — recipe + MCP have their own
//     audit story via stdout JSON capture)
//
// In short: prepare the inputs (Intent + Target + Store + State +
// IntentHash), call Apply, format the Result however the caller
// wants. Apply itself is a pure function modulo the file-system
// side effects of rendering + deploying.
package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/render"
	"github.com/tronprotocol/tron-deployment/internal/runtime"
	"github.com/tronprotocol/tron-deployment/internal/state"
	"github.com/tronprotocol/tron-deployment/internal/target"
)

// Options carries everything Apply needs that the caller is best
// positioned to assemble (intent, target, store, state, hash).
type Options struct {
	Intent     *intent.Intent
	Target     target.Target
	Store      *state.Store
	State      *state.DeploymentState
	IntentHash string

	// Existing is store.GetNode(state, intent.Name) at call time, or
	// nil when there's no prior managed node by that name. Carrying
	// it here avoids a redundant lookup inside Apply.
	Existing *state.ManagedNode

	// TemplateDir is the optional override for the embedded HOCON
	// template tree. Empty string uses the embedded copy (the common
	// case for production builds).
	TemplateDir string

	// DeploymentsDir is the on-disk root the docker runtime writes
	// rendered compose + .conf files under (typically ~/.trond/deployments).
	DeploymentsDir string

	// JDKVersion is honored if non-zero (lets callers skip the
	// `java -version` probe for known targets). 0 triggers detection.
	JDKVersion int

	// EnvVars are the Witness-key-style env vars that get passed
	// through to the docker container / systemd unit.
	EnvVars map[string]string

	// IntentPath is the on-disk path of the intent.yaml that produced
	// Intent. Used to resolve `build.source: ./relative/path` against
	// the intent file's directory per spec/002 FR-021. Optional when
	// the intent has no `build:` block.
	IntentPath string

	// Wait + WaitTimeout: when Wait is true, Apply blocks until the
	// node's HTTP API responds 2xx or WaitTimeout elapses. Wait
	// failures don't roll the deploy back; they surface in
	// Result.WaitError / Result.Ready.
	Wait        bool
	WaitTimeout time.Duration
}

// Result is the structured output of one Apply call. Stable JSON
// shape (matches schemas/output/apply.schema.json) so MCP / recipe /
// CLI presentations are interchangeable.
type Result struct {
	Name       string            `json:"name"`
	Outcome    string            `json:"outcome"` // created | updated | no_change
	IntentHash string            `json:"intent_hash"`
	ConfigHash string            `json:"config_hash"`
	Version    string            `json:"version"`
	Runtime    string            `json:"runtime"`
	Endpoints  map[string]string `json:"endpoints"`
	DurationMs int64             `json:"duration_ms"`

	// Ready / WaitedMs / WaitError are only set when Options.Wait was true.
	Ready     *bool  `json:"ready,omitempty"`
	WaitedMs  int64  `json:"waited_ms,omitempty"`
	WaitError string `json:"wait_error,omitempty"`

	// Build is populated when the intent carried a `build:` block.
	// Matches the build.Result JSON shape (schemas/output/build.schema.json).
	// Omitted from the result envelope when no build was needed
	// (image or pre-built jar path).
	Build *BuildSummary `json:"build,omitempty"`
}

// BuildSummary is the slice of build.Result the apply envelope
// surfaces. The full per-build manifest stays under
// ~/.trond/builds/manifest/<key>.json; this is what an agent sees
// inline with `trond apply -o json`.
type BuildSummary struct {
	CacheKey       string `json:"cache_key"`
	SourceRevision string `json:"source_revision"`
	Dirty          bool   `json:"dirty"`
	ArtifactPath   string `json:"artifact_path,omitempty"`
	ImageTag       string `json:"image_tag,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	// BuilderImage records which pinned JDK builder produced this
	// artifact (e.g. eclipse-temurin:8-jdk-jammy@sha256:...). Lets an
	// agent answer "what image built this?" without round-tripping
	// through `trond build inspect`.
	BuilderImage string `json:"builder_image,omitempty"`
	// Platform + JDKVersion let an agent answer "is this the amd64
	// JDK 8 build or the arm64 JDK 17 build?" inline. Both come from
	// the build.Result manifest.
	Platform   string `json:"platform,omitempty"`
	JDKVersion string `json:"jdk_version,omitempty"`
	CacheHit   bool   `json:"cache_hit"`
	DurationMs int64  `json:"duration_ms"`
}

// Apply runs the deploy phase. Returns a Result on success or
// partial-success (deploy succeeded, wait timed out); returns an error
// only when the deploy itself failed.
//
// Idempotency:
//
//  1. For intents WITHOUT a `build:` block, Apply short-circuits as a
//     no-op when opts.Existing.IntentHash == opts.IntentHash — same
//     intent.yaml content, nothing to do regardless of node status.
//  2. For intents WITH a `build:` block, the source tree can change
//     even when intent.yaml itself doesn't. In that case Apply first
//     resolves the build (cache-hit-fast: ~150ms when nothing
//     changed) and short-circuits ONLY when BOTH the intent hash AND
//     the resolved build cache key match the existing managed node.
//     This closes the dev-loop bug where editing a `.java` file
//     without touching intent.yaml would silently no-op.
//
// Endpoints is reconstructed from the intent's port spec on the no-op
// path: apply.schema.json declares it as an object and emitting
// `null` (the zero value of map[string]string) violates the contract
// for agents that always expect host:port pairs to probe.
func Apply(ctx context.Context, opts Options) (*Result, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	start := time.Now()
	node := &opts.Intent.Nodes[0]

	// Fast path: intents without build blocks idempotency-gate on
	// intent hash alone (legacy behavior preserved).
	if node.Build == nil &&
		opts.Existing != nil && opts.Existing.IntentHash == opts.IntentHash {
		return noChangeResult(opts, nil, start), nil
	}

	// Resolve build before render. The artifact path it produces
	// feeds into the systemd unit (jar runtime) or the compose
	// image: field (docker runtime). Cache hit is < 200ms so this
	// is cheap to run unconditionally when build is present.
	// Failures surface as structured errors and abort apply.
	buildSummary, builtJarPath, builtImageTag, buildErr := resolveBuild(ctx, opts, node)
	if buildErr != nil {
		return nil, buildErr
	}

	// Build-aware idempotency: same intent AND same build cache key →
	// nothing changed end-to-end, even if the file timestamps moved.
	if node.Build != nil &&
		opts.Existing != nil &&
		opts.Existing.IntentHash == opts.IntentHash &&
		buildSummary != nil &&
		opts.Existing.BuildCacheKey == buildSummary.CacheKey {
		return noChangeResult(opts, buildSummary, start), nil
	}

	hocon, err := render.RenderHOCON(opts.TemplateDir, opts.Intent, node)
	if err != nil {
		return nil, fmt.Errorf("render hocon: %w", err)
	}
	configHash := sha256hex([]byte(hocon))

	jdk := opts.JDKVersion
	if jdk == 0 {
		jdk = detectJDK(ctx, opts.Target)
	}
	memGB := render.ParseMemoryGB(node.Resources.Memory)
	if memGB == 0 {
		memGB = 16
	}
	jvmArgs := render.JVMArgsString(memGB, jdk, node.JVM)

	runtimeType := opts.Intent.Target.Runtime
	if runtimeType == "" {
		// cobra apply always pipes the intent through intent.Parse →
		// ApplyDefaults, so Runtime should already be filled. This
		// fallback covers programmatic callers (recipe, MCP, ad-hoc
		// tests) that bypass ApplyDefaults — defer to the shared
		// rule (intent.DefaultRuntime) so they get the same "docker
		// unless build present → jar" behavior, not a silently
		// drifted local default.
		runtimeType = intent.DefaultRuntime(opts.Intent)
	}

	deployOpts := runtime.DeployOpts{
		Name:       opts.Intent.Name,
		ConfigData: []byte(hocon),
		EnvVars:    opts.EnvVars,
	}

	switch runtimeType {
	case "docker":
		deployOpts.ComposeData = []byte(render.RenderCompose(opts.Intent.Name, opts.Intent, node, "", jvmArgs))
		rt := runtime.NewDockerRuntime(opts.Target, opts.DeploymentsDir)
		if err := rt.Deploy(ctx, deployOpts); err != nil {
			return nil, fmt.Errorf("docker deploy: %w", err)
		}
	case "jar":
		jarPath := builtJarPath // empty when node has no `build:` block
		deployOpts.SystemdData = []byte(render.RenderSystemdUnit(opts.Intent, node, jvmArgs, jarPath, ""))
		// JarPath still points at the install_path location for `mkdir -p` /
		// config layout; only the systemd ExecStart references jarPath above.
		deployOpts.JarPath = filepath.Join(node.InstallPath, "FullNode.jar")
		if node.Jar != nil {
			deployOpts.JarURL = node.Jar.URL
			deployOpts.JarSHA256 = node.Jar.SHA256
		}
		rt := runtime.NewJarRuntime(opts.Target)
		if err := rt.Deploy(ctx, deployOpts); err != nil {
			return nil, fmt.Errorf("jar deploy: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeType)
	}

	// Persist into state.
	managed := state.ManagedNode{
		Name:       opts.Intent.Name,
		IntentHash: opts.IntentHash,
		ConfigHash: configHash,
		Version:    node.Version,
		Target: state.NodeTarget{
			Type:         opts.Intent.Target.Type,
			Host:         opts.Intent.Target.Host,
			User:         opts.Intent.Target.User,
			Port:         opts.Intent.Target.Port,
			IdentityFile: opts.Intent.Target.IdentityFile,
		},
		Runtime:     runtimeType,
		Status:      "running",
		LastApplied: time.Now().UTC(),
		HTTPPort:    node.Ports.HTTP,
		GRPCPort:    node.Ports.GRPC,
		Labels:      node.Labels,
		InstallPath: node.InstallPath,
	}
	outcome := "created"
	if opts.Existing != nil {
		managed.PreviousVersion = opts.Existing.Version
		outcome = "updated"
	}
	if buildSummary != nil {
		managed.BuildCacheKey = buildSummary.CacheKey
	}
	opts.Store.UpsertNode(opts.State, managed)
	if err := opts.Store.Save(opts.State); err != nil {
		return nil, fmt.Errorf("save state: %w", err)
	}

	deployedMs := time.Since(start).Milliseconds()
	res := &Result{
		Name:       opts.Intent.Name,
		Outcome:    outcome,
		IntentHash: opts.IntentHash,
		ConfigHash: configHash,
		Version:    node.Version,
		Runtime:    runtimeType,
		Endpoints: map[string]string{
			"http": fmt.Sprintf("http://127.0.0.1:%d", node.Ports.HTTP),
			"grpc": fmt.Sprintf("127.0.0.1:%d", node.Ports.GRPC),
		},
		DurationMs: deployedMs,
		Build:      buildSummary,
	}
	_ = builtImageTag // consumed by Phase 3 (docker runtime + image artifact)

	if opts.Wait {
		waitErr := WaitForReady(ctx, opts.Target, opts.Intent.Name, node.Ports.HTTP, opts.WaitTimeout)
		res.WaitedMs = time.Since(start).Milliseconds() - deployedMs
		ready := waitErr == nil
		res.Ready = &ready
		if waitErr != nil {
			res.WaitError = waitErr.Error()
		}
	}
	return res, nil
}

func validateOptions(o Options) error {
	switch {
	case o.Intent == nil:
		return fmt.Errorf("Intent is required")
	case len(o.Intent.Nodes) == 0:
		return fmt.Errorf("Intent.Nodes is empty")
	case o.Target == nil:
		return fmt.Errorf("Target is required")
	case o.Store == nil:
		return fmt.Errorf("Store is required")
	case o.State == nil:
		return fmt.Errorf("State is required")
	case o.IntentHash == "":
		return fmt.Errorf("IntentHash is required")
	}
	// Defense-in-depth: callers SHOULD pre-validate via intent.Validate()
	// (cobra apply does), but recipe / MCP / programmatic callers may
	// bypass that. Enforce the artifact-source mutex here so a malformed
	// caller can't deploy a node with both Build and Image both wired.
	// Spec/002 FR-005.
	n := &o.Intent.Nodes[0]
	sources := 0
	if n.Build != nil {
		sources++
	}
	if n.Image != "" {
		sources++
	}
	if n.Jar != nil {
		sources++
	}
	if sources > 1 {
		return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
			"node %q: build, image, jar are mutually exclusive (pick one artifact source)", n.Type)
	}

	// Phase 2 only wires artifact=jar end-to-end. The artifact_kind
	// must match the runtime, otherwise we'd render a docker compose
	// with `image: ""` (because the Image default is suppressed when
	// Build is present) or a systemd unit pointing at a non-existent
	// JAR. Reject the mismatch up-front. Phase 3 lifts this when
	// the docker runtime learns to consume artifact=image.
	if n.Build != nil {
		rt := o.Intent.Target.Runtime
		if rt == "" {
			rt = "jar" // build present → defaults to jar (matches applyTargetDefaults)
		}
		artifact := n.Build.Artifact
		if artifact == "" {
			artifact = "jar"
		}
		switch {
		case rt == "docker" && artifact == "jar":
			return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
				"node %q: target.runtime=docker requires build.artifact=image (Phase 3 work); use target.runtime=jar for now", n.Type)
		case rt == "jar" && artifact == "image":
			return output.NewErrorf("VALIDATION_ERROR", output.ExitValidationError,
				"node %q: target.runtime=jar cannot consume build.artifact=image — set build.artifact=jar or switch runtime", n.Type)
		}
	}
	return nil
}

// sha256hex hashes a byte slice as lower-case hex. Mirrors the helper
// in cmd/apply.go; duplicated here so this package has no cmd import.
// noChangeResult constructs the no-op `Outcome: "no_change"`
// envelope shared between the no-build fast path and the
// build-aware short-circuit. Threading buildSummary keeps the result
// consistent regardless of which gate fired — agents always see
// the same shape.
//
// PRECONDITION: opts.Existing must be non-nil. Both callers (the
// pre-build no_change gate and the build-aware gate) check this
// before invoking — the helper itself trusts the contract to keep
// the body straight-line.
func noChangeResult(opts Options, buildSummary *BuildSummary, start time.Time) *Result {
	ports := opts.Intent.Nodes[0].Ports
	return &Result{
		Name:       opts.Intent.Name,
		Outcome:    "no_change",
		IntentHash: opts.IntentHash,
		ConfigHash: opts.Existing.ConfigHash,
		Version:    opts.Existing.Version,
		Runtime:    opts.Existing.Runtime,
		Endpoints: map[string]string{
			"http": fmt.Sprintf("http://127.0.0.1:%d", ports.HTTP),
			"grpc": fmt.Sprintf("127.0.0.1:%d", ports.GRPC),
		},
		DurationMs: time.Since(start).Milliseconds(),
		Build:      buildSummary,
	}
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// IntentHashFromBytes is a convenience for callers that already hold
// the raw intent.yaml bytes (cmd/apply.go has them after os.ReadFile;
// MCP has them after reading the file in a tool handler).
func IntentHashFromBytes(data []byte) string {
	return sha256hex(data)
}

// detectJDK probes the target for an installed Java version. Returns
// 17 when detection fails — most TRON 4.x builds ship with JDK 17
// tuning defaults and the G1 args we select are safe across modern
// JDKs. Mirrors cmd/apply.go's helper. Uses stdlib strings/strconv
// directly; the earlier inline-helpers version had subtle behavioral
// drift from strconv.Atoi (e.g. silently returning 0 on whitespace).
func detectJDK(ctx context.Context, tgt target.Target) int {
	out, err := tgt.Exec(ctx, "java", "-version")
	if err != nil {
		return 17
	}
	s := string(out)
	idx := strings.IndexByte(s, '"')
	if idx < 0 {
		return 17
	}
	rest := s[idx+1:]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return 17
	}
	ver := strings.TrimPrefix(rest[:end], "1.")
	if dot := strings.IndexByte(ver, '.'); dot > 0 {
		ver = ver[:dot]
	}
	n, err := strconv.Atoi(strings.TrimSpace(ver))
	if err != nil || n <= 0 {
		return 17
	}
	return n
}
