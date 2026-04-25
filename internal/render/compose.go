package render

import (
	"fmt"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// RenderCompose generates a docker-compose.yaml from the intent.
//
// name is the deployment-unique identifier used for the service, container,
// volume, and config-file basename. Single-node deploys pass intent.Name;
// `network create` passes the per-node "<network>-node<i>" so multi-node
// networks don't collide on container_name.
func RenderCompose(name string, i *intent.Intent, node *intent.NodeSpec, configPath string, jvmArgs string) string {
	if name == "" {
		name = i.Name
	}

	image := node.Image
	if node.Version != "" && node.Version != "latest" {
		image = fmt.Sprintf("%s:%s", node.Image, node.Version)
	}

	ports := renderComposePorts(node)
	memory := node.Resources.Memory
	if memory == "" {
		memory = "16GB"
	}
	// Convert memory format: "16GB" → "16g"
	memoryLimit := strings.ToLower(strings.ReplaceAll(memory, "B", ""))

	var sb strings.Builder
	sb.WriteString("services:\n")
	sb.WriteString(fmt.Sprintf("  %s:\n", name))
	sb.WriteString(fmt.Sprintf("    image: %s\n", image))
	sb.WriteString(fmt.Sprintf("    container_name: %s\n", name))
	sb.WriteString("    restart: unless-stopped\n")
	sb.WriteString("    ports:\n")
	for _, p := range ports {
		sb.WriteString(fmt.Sprintf("      - \"%s\"\n", p))
	}
	sb.WriteString("    volumes:\n")
	sb.WriteString(fmt.Sprintf("      - ./%s.conf:/etc/tron/config.conf:ro\n", name))
	sb.WriteString(fmt.Sprintf("      - %s-data:/data\n", name))
	if jvmArgs != "" {
		sb.WriteString("    environment:\n")
		sb.WriteString(fmt.Sprintf("      - JAVA_OPTS=%s\n", jvmArgs))
	}

	if node.Type == "witness" && node.WitnessKeyEnv != "" {
		if jvmArgs == "" {
			sb.WriteString("    environment:\n")
		}
		sb.WriteString(fmt.Sprintf("      - WITNESS_PRIVATE_KEY=${%s}\n", node.WitnessKeyEnv))
	}

	sb.WriteString("    deploy:\n")
	sb.WriteString("      resources:\n")
	sb.WriteString("        limits:\n")
	sb.WriteString(fmt.Sprintf("          memory: %s\n", memoryLimit))
	sb.WriteString("    command:\n")
	sb.WriteString("      - \"java\"\n")
	sb.WriteString("      - \"-jar\"\n")
	sb.WriteString("      - \"/usr/local/tron/FullNode.jar\"\n")
	sb.WriteString("      - \"-c\"\n")
	sb.WriteString("      - \"/etc/tron/config.conf\"\n")

	if node.Type == "witness" {
		sb.WriteString("      - \"--witness\"\n")
	}

	sb.WriteString("\n")
	sb.WriteString("volumes:\n")
	sb.WriteString(fmt.Sprintf("  %s-data:\n", name))

	return sb.String()
}

func renderComposePorts(node *intent.NodeSpec) []string {
	ports := []string{
		fmt.Sprintf("%d:%d", node.Ports.HTTP, node.Ports.HTTP),
		fmt.Sprintf("%d:%d", node.Ports.GRPC, node.Ports.GRPC),
		fmt.Sprintf("%d:%d", node.Ports.P2P, node.Ports.P2P),
	}

	if node.Features.JSONRPC != nil && *node.Features.JSONRPC {
		ports = append(ports, fmt.Sprintf("%d:%d", node.Ports.JSONRPC, node.Ports.JSONRPC))
	}

	if node.Features.Metrics != nil && *node.Features.Metrics {
		ports = append(ports, fmt.Sprintf("%d:%d", node.Ports.Metrics, node.Ports.Metrics))
	}

	return ports
}
