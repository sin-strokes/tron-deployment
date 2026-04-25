package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/render"
)

var renderOutputDir string

var renderCmd = &cobra.Command{
	Use:   "render <intent-path>",
	Short: "Render configuration from an intent file",
	Long:  "Render HOCON config and docker-compose/systemd files from an intent, writing to stdout or --output-dir.",
	Args:  cobra.ExactArgs(1),
	RunE:  runRender,
}

func init() {
	renderCmd.Flags().StringVar(&renderOutputDir, "output-dir", "", "Directory to write rendered files (default: stdout)")
}

func runRender(cmd *cobra.Command, args []string) error {
	intentPath := args[0]
	outputFmt, _ := cmd.Flags().GetString("output")

	_ = outputFmt
	parsed, err := intent.Load(intentPath)
	if err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	// Find templates directory (relative to binary or working directory)
	templateDir := findTemplateDir()

	for i, node := range parsed.Nodes {
		// Render HOCON config
		hocon, err := render.RenderHOCON(templateDir, parsed, &node)
		if err != nil {
			return output.NewError("RENDER_ERROR", output.ExitGeneralError, err.Error())
		}

		// Render JVM args. Without a live target we can't probe JDK or real
		// host memory, so we size from the intent's resources.memory and
		// default to JDK 17 — both are safe static assumptions for the
		// `config render` preview path.
		memGB := render.ParseMemoryGB(node.Resources.Memory)
		if memGB == 0 {
			memGB = 16
		}
		jvmArgs := render.JVMArgsString(memGB, 17, node.JVM)

		// Render runtime artifacts
		var composeYAML, systemdUnit string
		if parsed.Target.Runtime == "docker" || parsed.Target.Runtime == "" {
			composeYAML = render.RenderCompose(parsed.Name, parsed, &node, "", jvmArgs)
		}
		if parsed.Target.Runtime == "jar" {
			systemdUnit = render.RenderSystemdUnit(parsed, &node, jvmArgs, "", "")
		}

		if renderOutputDir != "" {
			if err := writeRenderedFiles(renderOutputDir, parsed.Name, i, hocon, composeYAML, systemdUnit); err != nil {
				return err
			}
		} else {
			// Write to stdout
			if i > 0 {
				fmt.Println("---")
			}
			fmt.Printf("# HOCON Config (node %d: %s)\n", i, node.Type)
			fmt.Println(hocon)
			if composeYAML != "" {
				fmt.Println("# docker-compose.yaml")
				fmt.Println(composeYAML)
			}
			if systemdUnit != "" {
				fmt.Println("# systemd unit")
				fmt.Println(systemdUnit)
			}
		}
	}

	return nil
}

func writeRenderedFiles(dir, name string, nodeIdx int, hocon, compose, systemd string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	configName := fmt.Sprintf("%s.conf", name)
	if err := os.WriteFile(filepath.Join(dir, configName), []byte(hocon), 0644); err != nil {
		return fmt.Errorf("write hocon: %w", err)
	}

	if compose != "" {
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte(compose), 0644); err != nil {
			return fmt.Errorf("write compose: %w", err)
		}
	}

	if systemd != "" {
		unitName := fmt.Sprintf("tron-%s.service", name)
		if err := os.WriteFile(filepath.Join(dir, unitName), []byte(systemd), 0644); err != nil {
			return fmt.Errorf("write systemd: %w", err)
		}
	}

	return nil
}

// findTemplateDir prefers the TROND_TEMPLATES_DIR env var, then falls back to
// ./templates. An empty return value tells render.RenderHOCON to use the
// embedded copy — release binaries work without any co-located files.
func findTemplateDir() string {
	if d := os.Getenv("TROND_TEMPLATES_DIR"); d != "" {
		return d
	}
	candidates := []string{"templates", "./templates"}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}
