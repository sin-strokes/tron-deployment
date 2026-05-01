package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/runtime"
)

var (
	logsTail   int
	logsFollow bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <node>",
	Short: "View logs from a node",
	Args:  cobra.ExactArgs(1),
	RunE:  runLogs,
}

func init() {
	logsCmd.Flags().IntVar(&logsTail, "tail", 100, "Number of lines to show")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	name := args[0]
	outputFmt, _ := cmd.Flags().GetString("output")

	nc, err := resolveNodeContext(name, outputFmt)
	if err != nil {
		return err
	}
	defer nc.Close()

	reader, err := nc.Runtime.Logs(cmd.Context(), name, runtime.LogOpts{
		Tail:   logsTail,
		Follow: logsFollow,
	})
	if err != nil {
		return exitWithError(outputFmt, "LOGS_ERROR", output.ExitGeneralError,
			fmt.Sprintf("Failed to get logs for %s: %v", name, err))
	}
	defer reader.Close()

	io.Copy(os.Stdout, reader)
	return nil
}
