package config

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var validateCmd = &cobra.Command{
	Use:   "validate <intent-path>",
	Short: "Validate an intent file",
	Long:  "Load and validate an intent YAML file against the schema. Exit 0 if valid, exit 2 if invalid.",
	Args:  cobra.ExactArgs(1),
	RunE:  runValidate,
}

func runValidate(cmd *cobra.Command, args []string) error {
	intentPath := args[0]
	outputFmt, _ := cmd.Flags().GetString("output")

	parsed, err := intent.Load(intentPath)
	if err != nil {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError, err.Error()).
			WithSuggestions(
				"Check the intent file syntax with: cat "+intentPath,
				"Refer to examples/ for valid intent files",
			)
	}

	result := map[string]any{
		"valid":   true,
		"name":    parsed.Name,
		"network": parsed.Network,
		"nodes":   len(parsed.Nodes),
	}

	if outputFmt == "json" {
		output.WriteJSON(os.Stdout, result)
	} else {
		fmt.Printf("Intent %q is valid (%s, %d node(s))\n", parsed.Name, parsed.Network, len(parsed.Nodes))
	}

	return nil
}
