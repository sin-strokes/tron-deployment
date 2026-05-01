package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tronprotocol/tron-deployment/internal/intent"
	"github.com/tronprotocol/tron-deployment/internal/render"
)

// registerConfigTools wires the read-only config-plane tools:
// validate, render, plan. These don't mutate any state — agents can
// freely use them to inspect what `apply` would do.

type intentPathArg struct {
	Path string `json:"path" jsonschema:"absolute path to an intent.yaml file"`
}

type renderArg struct {
	Path string `json:"path" jsonschema:"absolute path to an intent.yaml file"`
	Node int    `json:"node,omitempty" jsonschema:"render only the node at this index (omit to render all nodes)"`
}

func registerConfigTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "config_validate",
		Title:       "Validate intent file",
		Description: "Validates an intent.yaml file's shape and field constraints. Always run this before plan/apply. Equivalent to `trond config validate <path> -o json`.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, validateTool)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "config_render",
		Title:       "Render intent to HOCON + compose/systemd",
		Description: "Render the intent.yaml into the final java-tron HOCON config plus the compose / systemd file that would be written. Useful for previewing what apply would produce. Equivalent to `trond config render <path> -o json`.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, renderTool)
}

func validateTool(ctx context.Context, _ *mcp.CallToolRequest, args intentPathArg) (*mcp.CallToolResult, any, error) {
	parsed, err := intent.Load(args.Path)
	if err != nil {
		return errResult(err)
	}
	return jsonResult(map[string]any{
		"valid":      true,
		"name":       parsed.Name,
		"network":    parsed.Network,
		"node_count": len(parsed.Nodes),
	})
}

func renderTool(ctx context.Context, _ *mcp.CallToolRequest, args renderArg) (*mcp.CallToolResult, any, error) {
	parsed, err := intent.Load(args.Path)
	if err != nil {
		return errResult(err)
	}

	// templateDir resolution: empty → embedded templates. We don't
	// expose a `template_dir` arg because MCP clients usually live on
	// the user's laptop where the embedded templates are correct.
	templateDir := ""

	rendered := make([]map[string]any, 0, len(parsed.Nodes))
	for i := range parsed.Nodes {
		// args.Node uses 0 for "all" (default); 1-based index for filter.
		// So args.Node=2 → render only the second node (i=1).
		if args.Node != 0 && args.Node-1 != i {
			continue
		}
		node := &parsed.Nodes[i]
		hocon, err := render.RenderHOCON(templateDir, parsed, node)
		if err != nil {
			return errResult(err)
		}
		memGB := render.ParseMemoryGB(node.Resources.Memory)
		if memGB == 0 {
			memGB = 16
		}
		jvmArgs := render.JVMArgsString(memGB, 17, node.JVM)

		row := map[string]any{
			"index":    i,
			"name":     parsed.Name,
			"type":     node.Type,
			"hocon":    hocon,
			"jvm_args": jvmArgs,
		}
		runtime := parsed.Target.Runtime
		if runtime == "" {
			runtime = "docker"
		}
		switch runtime {
		case "docker":
			row["compose"] = render.RenderCompose(parsed.Name, parsed, node, "", jvmArgs)
		case "jar":
			row["systemd"] = render.RenderSystemdUnit(parsed, node, jvmArgs, "", "")
		}
		rendered = append(rendered, row)
	}
	return jsonResult(map[string]any{
		"name":    parsed.Name,
		"network": parsed.Network,
		"nodes":   rendered,
	})
}
