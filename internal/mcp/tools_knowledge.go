package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tronprotocol/tron-deployment/internal/knowledge"
)

// registerKnowledgeTools exposes the embedded knowledge corpus as two
// MCP tools: list-topics and get-topic. Cheap, zero-side-effect, no
// permissions needed.

type knowledgeTopicArg struct {
	Topic string `json:"topic" jsonschema:"name of the knowledge topic; use knowledge_list to see available topics"`
}

func registerKnowledgeTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "knowledge_list",
		Title:       "List embedded knowledge topics",
		Description: "Return the names of every embedded knowledge topic. Topics include node-types, troubleshooting, best-practices, config-reference, cloud-deployment, test-harness, snapshots, release-signatures, and more.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, knowledgeListTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "knowledge_get",
		Title:       "Read one knowledge topic",
		Description: "Return the full markdown contents of one knowledge topic. Prefer this over paraphrasing from training data when the user's question maps to a topic.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, knowledgeGetTool)
}

func knowledgeListTool(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
	return jsonResult(map[string]any{"topics": knowledge.Topics()})
}

func knowledgeGetTool(ctx context.Context, _ *mcp.CallToolRequest, args knowledgeTopicArg) (*mcp.CallToolResult, any, error) {
	body, err := knowledge.Get(args.Topic)
	if err != nil {
		return errResult(err)
	}
	// Markdown body verbatim — we surface as text content rather than
	// JSON so the client can render markdown correctly.
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: body}},
	}, body, nil
}
