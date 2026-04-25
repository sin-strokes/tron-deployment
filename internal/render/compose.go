package render

import (
	"fmt"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// Filesystem layout inside the official tronprotocol/java-tron image
// (defined by tron-docker tools/docker/Dockerfile + docker-entrypoint.sh):
//
//	/java-tron/                 — WORKDIR
//	/java-tron/bin/FullNode     — entrypoint binary
//	/java-tron/conf/            — host-mounted config dir (we drop the rendered
//	                              <name>_config.conf here)
//	/java-tron/output-directory — chain DB / state (must be persisted)
//	/java-tron/logs             — gc.log, tron.log (worth persisting for triage)
//
// Renderings before this aligned with neither the image nor the entrypoint:
// they pointed at /usr/local/tron/FullNode.jar (does not exist), mounted a
// /data volume the runtime never wrote to, and bypassed the entrypoint
// (./bin/docker-entrypoint.sh) by spelling out `java -jar`. Containers
// silently restart-looped as a result. This file is the corrected layout.
const (
	containerWorkdir   = "/java-tron"
	containerConfigDir = containerWorkdir + "/conf"
	containerDataDir   = containerWorkdir + "/output-directory"
	containerLogDir    = containerWorkdir + "/logs"
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

	containerConfigPath := fmt.Sprintf("%s/%s.conf", containerConfigDir, name)

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
	// HOCON config: read-only bind mount of the rendered file into /java-tron/conf.
	sb.WriteString(fmt.Sprintf("      - ./%s.conf:%s:ro\n", name, containerConfigPath))
	// Persistent state — the chain DB lives here, MUST be persisted.
	sb.WriteString(fmt.Sprintf("      - %s-data:%s\n", name, containerDataDir))
	// Logs — useful for post-mortem when a container crashes.
	sb.WriteString(fmt.Sprintf("      - %s-logs:%s\n", name, containerLogDir))

	// Witness key passthrough is the only env we set; the image's entrypoint
	// reads JVM args from the `-jvm "..."` command argument, not JAVA_OPTS.
	if node.Type == "witness" && node.WitnessKeyEnv != "" {
		sb.WriteString("    environment:\n")
		sb.WriteString(fmt.Sprintf("      - WITNESS_PRIVATE_KEY=${%s}\n", node.WitnessKeyEnv))
	}

	sb.WriteString("    deploy:\n")
	sb.WriteString("      resources:\n")
	sb.WriteString("        limits:\n")
	sb.WriteString(fmt.Sprintf("          memory: %s\n", memoryLimit))

	// Command goes to ./bin/docker-entrypoint.sh which prefixes
	// ./bin/FullNode and passes the rest. We let the entrypoint do that
	// rather than re-implementing it here.
	sb.WriteString("    command:\n")
	if jvmArgs != "" {
		// FullNode accepts JVM args via "-jvm <quoted single string>".
		sb.WriteString("      - \"-jvm\"\n")
		sb.WriteString(fmt.Sprintf("      - %q\n", "{"+jvmArgs+"}"))
	}
	sb.WriteString("      - \"-c\"\n")
	sb.WriteString(fmt.Sprintf("      - %q\n", containerConfigPath))
	if node.Type == "witness" {
		sb.WriteString("      - \"--witness\"\n")
	}

	sb.WriteString("\n")
	sb.WriteString("volumes:\n")
	sb.WriteString(fmt.Sprintf("  %s-data:\n", name))
	sb.WriteString(fmt.Sprintf("  %s-logs:\n", name))

	return sb.String()
}

// renderComposePorts produces the host:container port mappings.
//
// The P2P port also needs UDP exposed for kad-style peer discovery. HTTP /
// gRPC are TCP-only. Optional features (jsonrpc, metrics, solidity APIs)
// are appended only when the user explicitly enables them or sets a custom
// port.
func renderComposePorts(node *intent.NodeSpec) []string {
	ports := []string{
		fmt.Sprintf("%d:%d", node.Ports.HTTP, node.Ports.HTTP),
		fmt.Sprintf("%d:%d", node.Ports.GRPC, node.Ports.GRPC),
		fmt.Sprintf("%d:%d", node.Ports.P2P, node.Ports.P2P),
		fmt.Sprintf("%d:%d/udp", node.Ports.P2P, node.Ports.P2P),
	}

	if node.Features.JSONRPC != nil && *node.Features.JSONRPC {
		ports = append(ports, fmt.Sprintf("%d:%d", node.Ports.JSONRPC, node.Ports.JSONRPC))
	}

	if node.Features.Metrics != nil && *node.Features.Metrics {
		ports = append(ports, fmt.Sprintf("%d:%d", node.Ports.Metrics, node.Ports.Metrics))
	}

	return ports
}
