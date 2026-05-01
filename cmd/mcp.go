package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/mcp"
)

// mcpCmd runs trond as a Model Context Protocol server over stdio.
//
// Designed so chat-based / IDE-embedded agents (Claude Desktop, Cursor,
// Cline, Continue.dev, Zed AI, ChatGPT Apps) can call trond as
// structured tools. Configure once in the client; every tool then
// becomes a function the LLM can invoke directly.
//
// Example Claude Desktop config (~/.config/claude-desktop/config.json
// or %APPDATA%/Claude/config.json):
//
//	{
//	  "mcpServers": {
//	    "trond": { "command": "/usr/local/bin/trond", "args": ["mcp"] }
//	  }
//	}
//
// Once configured the agent can call:
//   - list, status, inspect, health, doctor, version
//   - config_validate, config_render, plan
//   - snapshot_sources, snapshot_list, snapshot_jobs, snapshot_download
//   - knowledge_list, knowledge_get
//
// `apply` is registered but currently routes back to a CLI-shell hint
// pending a refactor of cmd/apply.go's RunE into a pure function.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run trond as a Model Context Protocol server (stdio)",
	Long: `Start a Model Context Protocol (MCP) server over stdin/stdout.

MCP is the emerging standard for tools that AI agents call directly
(without going through a shell). Configure your MCP-aware client
(Claude Desktop, Cursor, Cline, Continue.dev, Zed AI, ChatGPT Apps)
to spawn 'trond mcp' and the agent gains access to every read-only
trond capability as a structured tool.

This subcommand reads JSON-RPC framed MCP messages from stdin and
writes responses to stdout. It blocks until the client disconnects.

For the broader contract that any agent calling trond should follow
(workflows, exit-code semantics, anti-patterns) see AGENTS.md at the
repo root.`,
	RunE: runMCP,
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}

func runMCP(cmd *cobra.Command, _ []string) error {
	// Hand the build-stamped version into the mcp package so the
	// version + doctor tools return the same string the CLI shows.
	mcp.SetVersionInfo(version, commit, buildTime)
	return mcp.Run(cmd.Context(), os.Stdin, os.Stdout, version)
}
