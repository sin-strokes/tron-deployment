package network

import (
	"fmt"
	"os"
	"path/filepath"
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

var createIntentPath string

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a private network from an intent file",
	Long:  "Deploy all nodes defined in the intent file, wiring peer connections via seed node config.",
	RunE:  runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createIntentPath, "intent", "", "Path to intent.yaml (required)")
	if err := createCmd.MarkFlagRequired("intent"); err != nil {
		panic(err)
	}
}

func runCreate(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")
	start := time.Now()

	_ = outputFmt // reserved for future format-specific output
	parsed, err := intent.Load(createIntentPath)
	if err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	// Resolve target
	var tgt target.Target
	switch parsed.Target.Type {
	case "ssh":
		t := target.NewSSHTarget(parsed.Target.Host, parsed.Target.Port, parsed.Target.User, parsed.Target.IdentityFile)
		if err := t.Connect(); err != nil {
			return output.NewError("TARGET_UNREACHABLE", output.ExitTargetUnreachable, err.Error())
		}
		defer t.Close()
		tgt = t
	default:
		tgt = target.NewLocalTarget()
	}

	templateDir := findTemplatesDir()
	workDir := paths.Deployments()

	store, _ := state.NewStore(paths.State())
	deployState, _ := store.Load()

	// Auto-wire peering between siblings before rendering. After
	// intent.Load() ports are final (defaults applied, auto_ports
	// resolved), so we can synthesise stable inter-container addresses.
	// Each node's network_overrides.active_peers is set to all OTHER
	// nodes' "<name>:<p2p_port>" — except when the user supplied an
	// explicit list, which we never override.
	autoWireActivePeers(parsed)

	var deployed []map[string]any

	for i, node := range parsed.Nodes {
		nodeName := fmt.Sprintf("%s-node%d", parsed.Name, i)

		hocon, err := render.RenderHOCON(templateDir, parsed, &node)
		if err != nil {
			return fmt.Errorf("render config for node %d: %w", i, err)
		}

		memGB := render.ParseMemoryGB(node.Resources.Memory)
		if memGB == 0 {
			memGB = 16
		}
		jvmArgs := render.JVMArgsString(memGB, 17, node.JVM)
		composeYAML := render.RenderCompose(nodeName, parsed, &node, "", jvmArgs)

		opts := runtime.DeployOpts{
			Name:        nodeName,
			ConfigData:  []byte(hocon),
			ComposeData: []byte(composeYAML),
		}

		rt := runtime.NewDockerRuntime(tgt, workDir)
		if err := rt.Deploy(cmd.Context(), opts); err != nil {
			deployed = append(deployed, map[string]any{
				"name":   nodeName,
				"type":   node.Type,
				"status": "error",
				"error":  err.Error(),
			})
			continue
		}

		// Capture the (post-defaults, post-auto-allocation) ports in state
		// so inspect / health / diagnose / events can target the right
		// host endpoint without re-reading the intent file.
		mn := state.ManagedNode{
			Name:    nodeName,
			Version: node.Version,
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
			Labels:      node.Labels,
		}
		store.UpsertNode(deployState, mn)

		deployed = append(deployed, map[string]any{
			"name": nodeName,
			"type": node.Type,
			"status": "running",
			"endpoints": map[string]string{
				"http": fmt.Sprintf("http://127.0.0.1:%d", node.Ports.HTTP),
				"grpc": fmt.Sprintf("127.0.0.1:%d", node.Ports.GRPC),
			},
		})
	}

	store.Save(deployState)
	writeAudit(auditEvent{Command: "network-create", Node: parsed.Name, Target: "local", Result: "success", Start: start})

	result := map[string]any{
		"network": parsed.Name,
		"nodes":   deployed,
	}
	output.WriteJSON(os.Stdout, result)
	return nil
}

// autoWireActivePeers fills each node's network_overrides.active_peers
// with the addresses of all OTHER nodes in the network. We only touch
// nodes whose active_peers is unset; the user can opt out by supplying
// even an empty list ([] explicitly, parsed as a non-nil zero-length
// slice). Addresses use the docker-compose container name as host so
// they resolve correctly inside the shared docker network.
//
// Why this is necessary: with auto_ports the rendered P2P port is no
// longer 18888, and the user can't know that port at intent-write time.
// seed.node lists alone aren't enough to keep peers connected when
// node.discovery is off, so node.active is the right field — and
// network create is the one command that knows enough about siblings
// to populate it deterministically.
func autoWireActivePeers(parsed *intent.Intent) {
	addresses := make([]string, len(parsed.Nodes))
	for i := range parsed.Nodes {
		nodeName := fmt.Sprintf("%s-node%d", parsed.Name, i)
		addresses[i] = fmt.Sprintf("%s:%d", nodeName, parsed.Nodes[i].Ports.P2P)
	}

	for i := range parsed.Nodes {
		// Skip nodes the user explicitly configured (even with []).
		if parsed.Nodes[i].NetworkOverrides.ActivePeers != nil {
			continue
		}
		var others []string
		for j, addr := range addresses {
			if j == i {
				continue
			}
			others = append(others, addr)
		}
		if len(others) == 0 {
			continue
		}
		parsed.Nodes[i].NetworkOverrides.ActivePeers = &others
	}
}

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
	return ""
}
