package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var (
	verifyIntentPath string
	verifyTimeout    time.Duration
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify a deployed node is healthy",
	Long:  "Post-deployment health gate. Polls the node's HTTP API until healthy or timeout.",
	RunE:  runVerify,
}

func init() {
	verifyCmd.Flags().StringVar(&verifyIntentPath, "intent", "", "Path to intent.yaml (required)")
	verifyCmd.Flags().DurationVar(&verifyTimeout, "timeout", 10*time.Minute, "Timeout for health check")
	mustMarkRequired(verifyCmd, "intent")
	rootCmd.AddCommand(verifyCmd)
}

func runVerify(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	parsed, err := intent.Load(verifyIntentPath)
	if err != nil {
		return exitWithError(outputFmt, "VALIDATION_ERROR", output.ExitValidationError, err.Error())
	}

	tgt, err := resolveTarget(parsed)
	if err != nil {
		return exitWithError(outputFmt, "TARGET_UNREACHABLE", output.ExitTargetUnreachable, err.Error())
	}
	if closer, ok := tgt.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	node := &parsed.Nodes[0]
	httpPort := node.Ports.HTTP
	if httpPort == 0 {
		httpPort = 8090
	}

	deadline := time.Now().Add(verifyTimeout)
	pollInterval := 10 * time.Second
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++
		url := fmt.Sprintf("http://127.0.0.1:%d/wallet/getnowblock", httpPort)
		out, err := tgt.Exec(cmd.Context(), "curl", "-s", "--max-time", "5", url)
		if err == nil {
			var block struct {
				BlockHeader struct {
					RawData struct {
						Number int64 `json:"number"`
					} `json:"raw_data"`
				} `json:"block_header"`
			}
			if json.Unmarshal(out, &block) == nil && block.BlockHeader.RawData.Number > 0 {
				result := map[string]any{
					"name":         parsed.Name,
					"verified":     true,
					"block_height": block.BlockHeader.RawData.Number,
					"attempts":     attempt,
				}
				writeResult(outputFmt, result)
				return nil
			}
		}

		if !quiet {
			fmt.Fprintf(os.Stderr, "Attempt %d: waiting for node to be ready...\n", attempt)
		}
		time.Sleep(pollInterval)
	}

	return exitWithError(outputFmt, "VERIFY_TIMEOUT", output.ExitGeneralError,
		fmt.Sprintf("Node %s not healthy after %s", parsed.Name, verifyTimeout),
		"Check node logs: trond logs "+parsed.Name,
		"Run diagnostics: trond diagnose "+parsed.Name)
}
