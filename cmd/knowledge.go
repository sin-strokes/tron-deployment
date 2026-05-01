package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/knowledge"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var knowledgeCmd = &cobra.Command{
	Use:   "knowledge [topic]",
	Short: "Query deployment guidance topics",
	Long:  "List available knowledge topics or display a specific topic's content.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runKnowledge,
}

func init() {
	rootCmd.AddCommand(knowledgeCmd)
}

func runKnowledge(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")

	if len(args) == 0 {
		topics := knowledge.Topics()
		if outputFmt == "json" {
			return output.WriteJSON(os.Stdout, map[string]any{
				"topics": topics,
			})
		}
		fmt.Println("Available topics:")
		for _, t := range topics {
			fmt.Printf("  - %s\n", t)
		}
		fmt.Println("\nUsage: trond knowledge <topic>")
		return nil
	}

	topic := args[0]
	content, err := knowledge.Get(topic)
	if err != nil {
		return exitWithError(outputFmt, "KNOWLEDGE_ERROR", output.ExitGeneralError, err.Error(),
			"List topics with: trond knowledge",
			"Available: "+strings.Join(knowledge.Topics(), ", "))
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"topic":   topic,
			"content": content,
		})
	}

	fmt.Print(content)
	return nil
}
