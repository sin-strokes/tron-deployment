package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
)

var execCmd = &cobra.Command{
	Use:   "exec <node> -- <cmd> [args...]",
	Short: "Execute a command on a managed node",
	Long: `Run an arbitrary command inside a managed node and stream its output.

For Docker runtime nodes the command runs inside the container via
"docker exec". For jar runtime nodes the command runs on the host where the
service is deployed (local target or SSH target depending on intent).

Use "--" to separate trond flags from the command line passed to the node:

    trond exec my-fullnode -- curl -s http://127.0.0.1:8090/wallet/getnowblock
    trond exec my-fullnode -- ls /var/log/tron`,
	Args: cobra.MinimumNArgs(2),
	RunE: runExec,
}

func init() {
	rootCmd.AddCommand(execCmd)
}

func runExec(cmd *cobra.Command, args []string) error {
	// Cobra parses global flags normally, then everything after "--" lands
	// in args verbatim. ArgsLenAtDash() tells us the position of "--": all
	// args before it are positional, after are the inner command.
	dashIdx := cmd.ArgsLenAtDash()
	var nodeName string
	var rest []string
	if dashIdx == -1 {
		// No "--" — fall back to "node is first arg, rest is the command".
		nodeName = args[0]
		rest = args[1:]
	} else {
		if dashIdx != 1 {
			return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
				"usage: trond exec <node> -- <cmd> [args...]")
		}
		nodeName = args[0]
		rest = args[1:]
	}
	if nodeName == "" || len(rest) == 0 {
		return output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"usage: trond exec <node> -- <cmd> [args...]")
	}

	outputFmt, _ := cmd.Flags().GetString("output")
	nc, err := resolveNodeContext(nodeName, outputFmt)
	if err != nil {
		return err
	}
	defer nc.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// For Docker nodes, wrap with `docker exec` so the command runs inside
	// the container. Jar nodes execute directly on the target host.
	var bin string
	var fullArgs []string
	if nc.Node.Runtime == "jar" {
		bin = rest[0]
		fullArgs = rest[1:]
	} else {
		bin = "docker"
		fullArgs = append([]string{"exec", nodeName}, rest...)
	}

	out, execErr := nc.Target.Exec(ctx, bin, fullArgs...)
	// Always emit captured output even on error — the caller usually wants it.
	os.Stdout.Write(out)
	if execErr != nil {
		return output.NewError("EXEC_ERROR", output.ExitGeneralError,
			fmt.Sprintf("exec on %s failed: %v", nodeName, execErr))
	}
	return nil
}
