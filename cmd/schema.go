package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/schema"
)

// schemaCmd dumps trond's CLI surface as JSON: every command, every
// flag, every documented output shape. Agents call this once at
// session start and from then on know the contract without reading
// --help text or AGENTS.md prose.
//
// Examples:
//
//	trond schema                      # full manifest, human text
//	trond schema -o json              # full manifest, JSON
//	trond schema apply -o json        # one command's full descriptor
//	trond schema --output-only apply  # just the JSON Schema for that command's output
var schemaCmd = &cobra.Command{
	Use:   "schema [command-path]",
	Short: "Dump the trond CLI manifest (commands, flags, output JSON Schemas)",
	Long: `Emit a structured description of the entire trond CLI surface — every
subcommand with its flags, types, defaults, examples, and (where
documented) the JSON Schema for its --output json result.

Designed for AI agents and tooling that need a machine-readable
contract instead of parsing --help text. Pin against the
schema_version field for stability.

Examples:

  trond schema                                 # full manifest
  trond schema -o json | jq '.commands[].full_name'
  trond schema apply -o json                   # single command
  trond schema apply --output-only -o json     # output schema only
`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSchema,
}

var schemaOutputOnly bool

func init() {
	schemaCmd.Flags().BoolVar(&schemaOutputOnly, "output-only", false,
		"Print only the output JSON Schema for the selected command")
	rootCmd.AddCommand(schemaCmd)
}

func runSchema(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	manifest := schema.Build(rootCmd, nil)

	// No subpath: full manifest.
	if len(args) == 0 {
		if schemaOutputOnly {
			return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
				"--output-only requires a command path; e.g. `trond schema apply --output-only`")
		}
		return emitManifest(outputFmt, manifest)
	}

	// One subpath: locate it.
	pathParts := append([]string{rootCmd.Name()}, strings.Fields(args[0])...)
	full := strings.Join(pathParts, " ")
	cmdNode := findCommand(manifest.Commands, full)
	if cmdNode == nil {
		return output.NewError("NOT_FOUND", output.ExitGeneralError,
			fmt.Sprintf("no command %q in trond CLI", args[0])).
			WithSuggestions("Run `trond schema -o json | jq '.commands[].full_name'` to list available commands")
	}

	if schemaOutputOnly {
		if cmdNode.OutputSchema == nil {
			return output.NewError("NOT_FOUND", output.ExitGeneralError,
				fmt.Sprintf("command %q has no documented output schema", full))
		}
		return emitSingle(outputFmt, cmdNode.OutputSchema)
	}
	return emitCommand(outputFmt, *cmdNode)
}

func findCommand(commands []schema.Command, full string) *schema.Command {
	for i := range commands {
		if commands[i].FullName == full {
			return &commands[i]
		}
		if c := findCommand(commands[i].Subcommands, full); c != nil {
			return c
		}
	}
	return nil
}

func emitManifest(format string, m schema.Manifest) error {
	if format == "json" {
		return jsonStdout(m)
	}
	fmt.Printf("trond CLI manifest\n")
	fmt.Printf("schema_version: %s\n\n", m.SchemaVersion)
	printCommands(m.Commands, 0)
	return nil
}

func emitCommand(format string, c schema.Command) error {
	if format == "json" {
		return jsonStdout(c)
	}
	fmt.Printf("%s\n", c.FullName)
	if c.Short != "" {
		fmt.Printf("  %s\n", c.Short)
	}
	if c.Long != "" {
		fmt.Printf("\n  %s\n", strings.ReplaceAll(c.Long, "\n", "\n  "))
	}
	if len(c.Flags) > 0 {
		fmt.Println("\nFlags:")
		for _, f := range c.Flags {
			req := ""
			if f.Required {
				req = " (required)"
			}
			fmt.Printf("  --%s [%s]%s — %s\n", f.Name, f.Type, req, f.Usage)
		}
	}
	if c.OutputSchema != nil {
		fmt.Printf("\nOutput schema: %s\n", c.OutputSchemaURL)
	}
	if len(c.Subcommands) > 0 {
		fmt.Println("\nSubcommands:")
		for _, sub := range c.Subcommands {
			fmt.Printf("  %s — %s\n", sub.FullName, sub.Short)
		}
	}
	return nil
}

func emitSingle(format string, doc map[string]any) error {
	if format == "json" {
		return jsonStdout(doc)
	}
	// In text mode we still emit JSON because schemas are JSON; pretty-print.
	return jsonStdout(doc)
}

func printCommands(cmds []schema.Command, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, c := range cmds {
		marker := ""
		if c.OutputSchema != nil {
			marker = " [schema]"
		}
		fmt.Printf("%s%s%s — %s\n", indent, c.FullName, marker, c.Short)
		printCommands(c.Subcommands, depth+1)
	}
}

func jsonStdout(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
