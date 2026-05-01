package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/render"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

var planIntentPath string

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Preview deployment changes without applying",
	Long:  "Show what trond apply would do: validate, render, diff against current state, output changes.",
	RunE:  runPlan,
}

func init() {
	planCmd.Flags().StringVar(&planIntentPath, "intent", "", "Path to intent.yaml (required)")
	mustMarkRequired(planCmd, "intent")
	rootCmd.AddCommand(planCmd)
}

type planChange struct {
	Type            string `json:"type"`
	Field           string `json:"field"`
	From            any    `json:"from,omitempty"`
	To              any    `json:"to,omitempty"`
	RestartRequired bool   `json:"restart_required"`
}

func runPlan(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	// 1. Load + validate
	parsed, err := intent.Load(planIntentPath)
	if err != nil {
		return exitWithError(outputFmt, "VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	// 2. Compute intent hash
	intentData, _ := os.ReadFile(planIntentPath)
	intentHash := sha256hex(intentData)

	// 3. Load current state
	store, err := state.NewStore(statePath())
	if err != nil {
		return exitWithError(outputFmt, "STATE_ERROR", output.ExitGeneralError, err.Error())
	}

	deployState, err := store.Load()
	if err != nil {
		return exitWithError(outputFmt, "STATE_ERROR", output.ExitGeneralError, err.Error())
	}

	existing := store.GetNode(deployState, parsed.Name)

	// 4. Render config to compute config hash
	templateDir := findTemplatesDir()
	node := &parsed.Nodes[0]

	hoconConfig, err := render.RenderHOCON(templateDir, parsed, node)
	if err != nil {
		return exitWithError(outputFmt, "RENDER_ERROR", output.ExitGeneralError, err.Error())
	}
	configHash := sha256hex([]byte(hoconConfig))

	// 5. Diff
	var changes []planChange
	destructive := false
	downtime := 0

	if existing == nil {
		// New deployment
		changes = append(changes, planChange{
			Type:  "create",
			Field: "node",
			To:    parsed.Name,
		})
	} else {
		// Check for changes
		if existing.IntentHash != intentHash {
			changes = append(changes, planChange{
				Type:            "update",
				Field:           "intent",
				From:            existing.IntentHash[:12] + "...",
				To:              intentHash[:12] + "...",
				RestartRequired: true,
			})
		}
		if existing.ConfigHash != configHash {
			changes = append(changes, planChange{
				Type:            "update",
				Field:           "config",
				From:            existing.ConfigHash[:12] + "...",
				To:              configHash[:12] + "...",
				RestartRequired: true,
			})
			downtime = 30 // Estimated restart time
		}
		if existing.Version != node.Version && node.Version != "latest" {
			changes = append(changes, planChange{
				Type:            "update",
				Field:           "version",
				From:            existing.Version,
				To:              node.Version,
				RestartRequired: true,
			})
			downtime = 60
		}
	}

	currentState := "not deployed"
	if existing != nil {
		currentState = existing.Status
	}

	runtimeType := parsed.Target.Runtime
	if runtimeType == "" {
		runtimeType = "docker"
	}

	result := map[string]any{
		"name":                       parsed.Name,
		"current_state":              currentState,
		"desired_state":              "running",
		"changes":                    changes,
		"destructive":                destructive,
		"estimated_downtime_seconds": downtime,
		"runtime":                    runtimeType,
		"network":                    parsed.Network,
	}

	if outputFmt == "json" {
		output.WriteJSON(os.Stdout, result)
	} else {
		printPlanText(result, changes)
	}

	return nil
}

func printPlanText(result map[string]any, changes []planChange) {
	fmt.Printf("Node:    %s\n", result["name"])
	fmt.Printf("Current: %s\n", result["current_state"])
	fmt.Printf("Desired: %s\n", result["desired_state"])
	fmt.Printf("Runtime: %s\n", result["runtime"])
	fmt.Println()

	if len(changes) == 0 {
		fmt.Println("No changes. Infrastructure is up-to-date.")
		return
	}

	fmt.Printf("Changes (%d):\n", len(changes))
	for _, c := range changes {
		switch c.Type {
		case "create":
			fmt.Printf("  + %s: %v\n", c.Field, c.To)
		case "update":
			fmt.Printf("  ~ %s: %v → %v", c.Field, c.From, c.To)
			if c.RestartRequired {
				fmt.Print(" (restart required)")
			}
			fmt.Println()
		case "delete":
			fmt.Printf("  - %s: %v\n", c.Field, c.From)
		}
	}

	if dt, ok := result["estimated_downtime_seconds"].(int); ok && dt > 0 {
		fmt.Printf("\nEstimated downtime: %ds\n", dt)
	}
}
