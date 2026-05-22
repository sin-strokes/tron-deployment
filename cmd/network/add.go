package network

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/render"
	"github.com/tronprotocol/tron-deployment/internal/runtime"
	"github.com/tronprotocol/tron-deployment/internal/state"
	"github.com/tronprotocol/tron-deployment/internal/target"
)

var (
	addNetworkName string
	addIntentPath  string
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a node to an existing private network",
	Long: `Append one node to an already-running private network.

The intent file may describe just the node (single entry under nodes:) — the
top-level name/network/target are taken from the existing enclave when the
intent omits them. The new node is named "<network>-node<i>" with i chosen
as the next free index.`,
	RunE: runAdd,
}

func init() {
	addCmd.Flags().StringVar(&addNetworkName, "network", "", "Network name (the prefix used by 'network create')")
	addCmd.Flags().StringVar(&addIntentPath, "intent", "", "Path to a single-node intent.yaml")
	if err := addCmd.MarkFlagRequired("network"); err != nil {
		panic(err)
	}
	if err := addCmd.MarkFlagRequired("intent"); err != nil {
		panic(err)
	}
	Cmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	start := time.Now()

	parsed, err := intent.Load(addIntentPath)
	if err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}
	if len(parsed.Nodes) != 1 {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"network add expects an intent with exactly one node entry")
	}
	node := &parsed.Nodes[0]

	// Pick the next free index. Existing entries are "<network>-node<N>"; we
	// rescan state instead of trusting any in-memory counter so the operation
	// is safe to retry.
	store, err := state.NewStore(paths.State())
	if err != nil {
		return err
	}
	deployState, err := store.Load()
	if err != nil {
		return err
	}

	prefix := addNetworkName + "-node"
	nextIdx := 0
	for _, n := range deployState.Nodes {
		if !strings.HasPrefix(n.Name, prefix) {
			continue
		}
		var idx int
		if _, err := fmt.Sscanf(n.Name, prefix+"%d", &idx); err == nil && idx >= nextIdx {
			nextIdx = idx + 1
		}
	}
	nodeName := fmt.Sprintf("%s%d", prefix, nextIdx)

	// Resolve target by type. SECURITY: previously this always built a
	// LocalTarget regardless of intent.target.type, which silently sent
	// SSH-intended deploys to the local host (and combined with any YAML
	// injection in the rendered compose, that ran on the operator's
	// machine instead of the remote target).
	var tgt target.Target
	switch parsed.Target.Type {
	case "ssh":
		s := target.NewSSHTarget(parsed.Target.Host, parsed.Target.Port,
			parsed.Target.User, parsed.Target.IdentityFile)
		if err := s.Connect(); err != nil {
			return output.NewError("TARGET_UNREACHABLE", output.ExitTargetUnreachable, err.Error())
		}
		tgt = s
	default:
		tgt = target.NewLocalTarget()
	}
	defer func() {
		if c, ok := any(tgt).(interface{ Close() error }); ok {
			c.Close()
		}
	}()

	templateDir := findTemplatesDir()
	hocon, err := render.RenderHOCON(templateDir, parsed, node)
	if err != nil {
		return fmt.Errorf("render config: %w", err)
	}

	memGB := render.ParseMemoryGB(node.Resources.Memory)
	if memGB == 0 {
		memGB = 16
	}
	jvmArgs := render.JVMArgsString(memGB, 17, node.JVM)
	composeYAML := render.RenderCompose(nodeName, parsed, node, "", jvmArgs, "")

	rt := runtime.NewDockerRuntime(tgt, paths.Deployments())
	opts := runtime.DeployOpts{
		Name:        nodeName,
		ConfigData:  []byte(hocon),
		ComposeData: []byte(composeYAML),
	}
	if err := rt.Deploy(cmd.Context(), opts); err != nil {
		return output.NewError("DEPLOY_ERROR", output.ExitGeneralError, err.Error())
	}

	store.UpsertNode(deployState, state.ManagedNode{
		Name:    nodeName,
		Version: node.Version,
		// Persist the FULL target so subsequent stop/start/files/inspect
		// can rebuild the SSH connection. Earlier this only stored
		// Type, leaving Host/User/Port/IdentityFile blank — which then
		// silently fell through to LocalTarget on follow-up commands.
		Target: state.NodeTarget{
			Type:         parsed.Target.Type,
			Host:         parsed.Target.Host,
			User:         parsed.Target.User,
			Port:         parsed.Target.Port,
			IdentityFile: parsed.Target.IdentityFile,
		},
		Runtime:     "docker",
		Status:      "running",
		LastApplied: time.Now().UTC(),
		HTTPPort:    node.Ports.HTTP,
		GRPCPort:    node.Ports.GRPC,
		InstallPath: node.InstallPath,
		Labels:      node.Labels,
	})
	if err := store.Save(deployState); err != nil {
		return output.NewError("STATE_ERROR", output.ExitGeneralError,
			"failed to persist state: "+err.Error())
	}
	writeAudit(auditEvent{
		Command: "network-add",
		Node:    nodeName,
		Target:  parsed.Target.Type, // honors the intent target, not hardcoded "local"
		Result:  "success",
		Start:   start,
	})

	result := map[string]any{
		"network": addNetworkName,
		"added":   nodeName,
		"endpoints": map[string]string{
			"http": fmt.Sprintf("http://127.0.0.1:%d", node.Ports.HTTP),
			"grpc": fmt.Sprintf("127.0.0.1:%d", node.Ports.GRPC),
		},
	}
	output.WriteJSON(os.Stdout, result)
	return nil
}
