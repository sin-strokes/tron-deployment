package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/render"
	"github.com/tronprotocol/tron-deployment/internal/runtime"
	"github.com/tronprotocol/tron-deployment/internal/state"
	"github.com/tronprotocol/tron-deployment/internal/target"
)

var (
	applyIntentPath  string
	applyAutoApprove bool
	applyWait        bool
	applyWaitTimeout time.Duration
)

var applyCmd = &cobra.Command{
	Use:     "apply",
	Aliases: []string{"deploy"},
	Short:   "Deploy or update a node from an intent file",
	Long: `Apply deploys a node to the specified target based on the intent file.

Pipeline: validate → resolve target → acquire lock → render config →
diff against state → deploy → update state → release lock → output result.

This command is idempotent — running it again with the same intent produces no changes.`,
	RunE: runApply,
}

func init() {
	applyCmd.Flags().StringVar(&applyIntentPath, "intent", "", "Path to intent.yaml (required)")
	applyCmd.Flags().BoolVar(&applyAutoApprove, "auto-approve", false, "Skip confirmation for changes (CI mode)")
	applyCmd.Flags().BoolVar(&applyWait, "wait", false, "Block until the deployed node's HTTP API is reachable")
	applyCmd.Flags().DurationVar(&applyWaitTimeout, "wait-timeout", 5*time.Minute, "Total wait budget when --wait is set")
	mustMarkRequired(applyCmd, "intent")
	rootCmd.AddCommand(applyCmd)
}

func runApply(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")
	start := time.Now()

	// 1. Load + validate intent
	parsed, err := intent.Load(applyIntentPath)
	if err != nil {
		return exitWithError(outputFmt, "VALIDATION_ERROR", output.ExitValidationError, err.Error(),
			"Check intent file syntax", "Run: trond config validate "+applyIntentPath)
	}

	// 2. Resolve target
	tgt, err := resolveTarget(parsed)
	if err != nil {
		return exitWithError(outputFmt, "TARGET_UNREACHABLE", output.ExitTargetUnreachable, err.Error(),
			"Check SSH connectivity", "Verify Docker is running")
	}

	// 3. Acquire state lock
	dir := stateDir()
	lock := state.NewLock(dir)
	if err := lock.Acquire(); err != nil {
		return exitWithError(outputFmt, "LOCK_ERROR", output.ExitGeneralError, "Failed to acquire state lock: "+err.Error(),
			"Check if another trond process is running")
	}
	defer lock.Release()

	// 4. Load current state
	store, err := state.NewStore(statePath())
	if err != nil {
		return exitWithError(outputFmt, "STATE_ERROR", output.ExitGeneralError, err.Error())
	}

	deployState, err := store.Load()
	if err != nil {
		return exitWithError(outputFmt, "STATE_ERROR", output.ExitGeneralError, err.Error())
	}

	// 5. Compute intent hash
	intentData, _ := os.ReadFile(applyIntentPath)
	intentHash := sha256hex(intentData)

	// 6. Check for changes
	existing := store.GetNode(deployState, parsed.Name)
	if existing != nil && existing.IntentHash == intentHash && existing.Status == "running" {
		// No changes needed
		if !quiet {
			result := map[string]any{
				"name":    parsed.Name,
				"status":  "running",
				"changes": []any{},
				"message": "No changes detected",
			}
			writeResult(outputFmt, result)
		}
		return nil
	}

	// 7. Confirmation for changes on existing node (unless --auto-approve)
	if existing != nil && !applyAutoApprove {
		return exitWithError(outputFmt, "HUMAN_REQUIRED", output.ExitHumanRequired,
			fmt.Sprintf("Changes detected for node %q. Review with: trond plan --intent %s", parsed.Name, applyIntentPath),
			"Re-run with --auto-approve to apply changes",
			fmt.Sprintf("trond apply --intent %s --auto-approve", applyIntentPath))
	}

	// 8. Render config
	templateDir := findTemplatesDir()
	node := &parsed.Nodes[0] // US1: single node

	hoconConfig, err := render.RenderHOCON(templateDir, parsed, node)
	if err != nil {
		return exitWithError(outputFmt, "RENDER_ERROR", output.ExitGeneralError, "Failed to render config: "+err.Error())
	}
	configHash := sha256hex([]byte(hoconConfig))

	// Derive JVM heap sizing from the intent's resources.memory and detected JDK.
	// Default to 16GB / JDK 17 when unspecified — matches the table in
	// knowledge/best-practices.md.
	memGB := render.ParseMemoryGB(node.Resources.Memory)
	if memGB == 0 {
		memGB = 16
	}
	jdkVer := detectJDKVersion(cmd.Context(), tgt)
	jvmArgs := render.JVMArgsString(memGB, jdkVer, node.JVM)

	// 8. Deploy based on runtime
	runtimeType := parsed.Target.Runtime
	if runtimeType == "" {
		runtimeType = "docker"
	}

	deployOpts := runtime.DeployOpts{
		Name:       parsed.Name,
		ConfigData: []byte(hoconConfig),
		EnvVars:    resolveEnvVars(node),
	}

	var changes []map[string]string

	switch runtimeType {
	case "docker":
		composeYAML := render.RenderCompose(parsed.Name, parsed, node, "", jvmArgs)
		deployOpts.ComposeData = []byte(composeYAML)

		rt := runtime.NewDockerRuntime(tgt, deploymentsDir())
		if err := rt.Deploy(cmd.Context(), deployOpts); err != nil {
			return exitWithError(outputFmt, "DEPLOY_ERROR", output.ExitGeneralError, "Deploy failed: "+err.Error(),
				"Check Docker is running: docker info",
				"Check port availability")
		}
		changes = append(changes, map[string]string{"type": "deploy", "description": "Docker container deployed"})

	case "jar":
		systemdUnit := render.RenderSystemdUnit(parsed, node, jvmArgs, "", "")
		deployOpts.SystemdData = []byte(systemdUnit)
		deployOpts.JarPath = filepath.Join(node.InstallPath, "FullNode.jar")

		rt := runtime.NewJarRuntime(tgt)
		if err := rt.Deploy(cmd.Context(), deployOpts); err != nil {
			return exitWithError(outputFmt, "DEPLOY_ERROR", output.ExitGeneralError, "Deploy failed: "+err.Error())
		}
		changes = append(changes, map[string]string{"type": "deploy", "description": "Jar+systemd service deployed"})
	}

	// 9. Update state
	managedNode := state.ManagedNode{
		Name:       parsed.Name,
		IntentHash: intentHash,
		ConfigHash: configHash,
		Version:    node.Version,
		Target: state.NodeTarget{
			Type: parsed.Target.Type,
			Host: parsed.Target.Host,
			User: parsed.Target.User,
			Port: parsed.Target.Port,
		},
		Runtime:     runtimeType,
		Status:      "running",
		LastApplied: time.Now().UTC(),
		HTTPPort:    node.Ports.HTTP,
		GRPCPort:    node.Ports.GRPC,
	}
	if existing != nil {
		managedNode.PreviousVersion = existing.Version
	}
	store.UpsertNode(deployState, managedNode)

	if err := store.Save(deployState); err != nil {
		return exitWithError(outputFmt, "STATE_ERROR", output.ExitGeneralError, "Failed to save state: "+err.Error())
	}

	// 10. Output result
	duration := time.Since(start)
	result := map[string]any{
		"name":    parsed.Name,
		"status":  "running",
		"changes": changes,
		"runtime": runtimeType,
		"version": node.Version,
		"endpoints": map[string]string{
			"http": fmt.Sprintf("http://localhost:%d", node.Ports.HTTP),
			"grpc": fmt.Sprintf("localhost:%d", node.Ports.GRPC),
		},
		"duration_ms": duration.Milliseconds(),
	}

	writeAudit(auditEvent{
		Command:    "apply",
		Node:       parsed.Name,
		Target:     tgt.String(),
		IntentHash: intentHash,
		Result:     "success",
		Start:      start,
	})

	// --wait blocks until the node's HTTP API responds. The deploy itself
	// has already succeeded by this point — a wait failure does not roll
	// back, the test harness can still call diagnose / logs to investigate.
	if applyWait {
		waitErr := waitForNodeReady(cmd.Context(), tgt, parsed.Name, node.Ports.HTTP, applyWaitTimeout)
		result["waited_ms"] = time.Since(start).Milliseconds() - result["duration_ms"].(int64)
		if waitErr != nil {
			result["ready"] = false
			result["wait_error"] = waitErr.Error()
			if !quiet {
				writeResult(outputFmt, result)
			}
			return output.NewError("WAIT_TIMEOUT", output.ExitGeneralError,
				fmt.Sprintf("deploy succeeded but node %s did not become ready: %v", parsed.Name, waitErr)).
				WithSuggestions("trond logs "+parsed.Name, "trond diagnose "+parsed.Name)
		}
		result["ready"] = true
	}

	if !quiet {
		writeResult(outputFmt, result)
	}
	return nil
}

// waitForNodeReady probes the node's HTTP endpoint until it responds 2xx or
// the timeout expires. Reuses the same probe family as `trond wait --http`
// so behaviour is consistent.
func waitForNodeReady(ctx context.Context, tgt target.Target, name string, httpPort int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if httpPort == 0 {
		httpPort = 8090
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/wallet/getnowblock", httpPort)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var lastErr error
	for {
		_, err := tgt.Exec(ctx, "docker", "exec", name, "curl", "-fsS", "--max-time", "5", url)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%s (last probe error: %v)", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func resolveTarget(parsed *intent.Intent) (target.Target, error) {
	switch parsed.Target.Type {
	case "ssh":
		t := target.NewSSHTarget(parsed.Target.Host, parsed.Target.Port, parsed.Target.User, parsed.Target.IdentityFile)
		if err := t.Connect(); err != nil {
			return nil, err
		}
		return t, nil
	default:
		return target.NewLocalTarget(), nil
	}
}

func resolveEnvVars(node *intent.NodeSpec) map[string]string {
	env := make(map[string]string)
	if node.WitnessKeyEnv != "" {
		val := os.Getenv(node.WitnessKeyEnv)
		if val != "" {
			env[node.WitnessKeyEnv] = val
		}
	}
	return env
}

// findTemplatesDir returns an optional on-disk templates directory. The render
// package falls back to embedded templates if this is empty or missing, so
// this is only a convenience for local development against an un-committed
// template tree. Set TROND_TEMPLATES_DIR to override.
func findTemplatesDir() string {
	if d := os.Getenv("TROND_TEMPLATES_DIR"); d != "" {
		return d
	}
	candidates := []string{"templates", "./templates"}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(c, "main_net_config.conf")); err == nil {
				return c
			}
		}
	}
	return "" // Signals render to use embedded templates.
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// detectJDKVersion probes the target for an installed Java version. Returns 17
// when detection fails — most TRON 4.x builds ship with JDK 17 tuning defaults,
// and the G1 args it selects are safe across modern JDKs.
func detectJDKVersion(ctx context.Context, tgt target.Target) int {
	out, err := tgt.Exec(ctx, "java", "-version")
	if err != nil {
		return 17
	}
	// "openjdk version \"1.8.0_362\""  → 8
	// "openjdk version \"17.0.8\""      → 17
	s := string(out)
	if idx := strings.Index(s, `"`); idx >= 0 {
		rest := s[idx+1:]
		end := strings.Index(rest, `"`)
		if end > 0 {
			ver := rest[:end]
			if strings.HasPrefix(ver, "1.") {
				ver = ver[2:]
			}
			if dot := strings.Index(ver, "."); dot > 0 {
				ver = ver[:dot]
			}
			if n, err := strconv.Atoi(ver); err == nil {
				return n
			}
		}
	}
	return 17
}

// exitWithError returns a StructuredError for propagation through cobra RunE.
// The root Execute() function inspects the error, writes it in the requested
// format, and returns the correct exit code — this lets deferred cleanup
// (e.g. state locks) run before the process exits.
func exitWithError(_ string, code string, exitCode int, msg string, suggestions ...string) error {
	return output.NewError(code, exitCode, msg).WithSuggestions(suggestions...)
}

func writeResult(_ string, result any) {
	output.WriteJSON(os.Stdout, result)
}
