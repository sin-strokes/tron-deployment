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

		mn := state.ManagedNode{
			Name:    nodeName,
			Version: node.Version,
			Target: state.NodeTarget{
				Type: parsed.Target.Type,
			},
			Runtime:     "docker",
			Status:      "running",
			LastApplied: time.Now().UTC(),
		}
		store.UpsertNode(deployState, mn)

		deployed = append(deployed, map[string]any{
			"name":   nodeName,
			"type":   node.Type,
			"status": "running",
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
