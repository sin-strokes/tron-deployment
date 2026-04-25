package config

import (
	"github.com/spf13/cobra"
)

// Cmd is the parent `trond config` command.
var Cmd = &cobra.Command{
	Use:   "config",
	Short: "Validate, render, and inspect intent configurations",
}

func init() {
	Cmd.AddCommand(validateCmd)
	Cmd.AddCommand(renderCmd)
}
