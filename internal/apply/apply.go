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
	"time"

	"github.com/tronprotocol/tron-deployment/internal/intent"
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
}

// Apply runs the deploy phase. Returns a Result on success or
// partial-success (deploy succeeded, wait timed out); returns an error
// only when the deploy itself failed.
//
// Idempotency: when opts.Existing.IntentHash == opts.IntentHash, Apply
// short-circuits as a no-op (Outcome: "no_change") without rendering
// or touching the runtime. This mirrors cmd/apply.go's hash-gate
// behavior, but the gate's HUMAN_REQUIRED branch is the caller's
// concern (it's a UX policy, not a deploy invariant).
func Apply(ctx context.Context, opts Options) (*Result, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	start := time.Now()

	// No-op short-circuit. Same hash → nothing to do regardless of
	// the existing node's status. Callers that need "force redeploy
	// of a stopped node" pass IntentHash="" or simply omit Existing.
	if opts.Existing != nil && opts.Existing.IntentHash == opts.IntentHash {
		return &Result{
			Name:       opts.Intent.Name,
			Outcome:    "no_change",
			IntentHash: opts.IntentHash,
			ConfigHash: opts.Existing.ConfigHash,
			Version:    opts.Existing.Version,
			Runtime:    opts.Existing.Runtime,
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	node := &opts.Intent.Nodes[0]

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
		runtimeType = "docker"
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
		deployOpts.SystemdData = []byte(render.RenderSystemdUnit(opts.Intent, node, jvmArgs, "", ""))
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
	}

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
	return nil
}

// sha256hex hashes a byte slice as lower-case hex. Mirrors the helper
// in cmd/apply.go; duplicated here so this package has no cmd import.
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
// JDKs. Mirrors cmd/apply.go's helper.
func detectJDK(ctx context.Context, tgt target.Target) int {
	out, err := tgt.Exec(ctx, "java", "-version")
	if err != nil {
		return 17
	}
	s := string(out)
	idx := indexByte(s, '"')
	if idx < 0 {
		return 17
	}
	rest := s[idx+1:]
	end := indexByte(rest, '"')
	if end <= 0 {
		return 17
	}
	ver := rest[:end]
	if hasPrefix(ver, "1.") {
		ver = ver[2:]
	}
	if dot := indexByte(ver, '.'); dot > 0 {
		ver = ver[:dot]
	}
	n := atoiSafe(ver)
	if n == 0 {
		return 17
	}
	return n
}

// Tiny stdlib re-exports kept inline so the package imports stay
// trim and the functions read clearly.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
func atoiSafe(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
