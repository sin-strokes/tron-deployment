package render

import (
	"fmt"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// NetworkTemplate maps network names to their base config template file.
var NetworkTemplate = map[string]string{
	"mainnet": "main_net_config.conf",
	"nile":    "test_net_config.conf",
	"private": "private_net_config.conf",
}

// RenderHOCON loads the base template for the network and applies intent-driven overrides.
// Returns the final HOCON config as a string. templateDir may be empty, in
// which case the embedded template is used.
func RenderHOCON(templateDir string, i *intent.Intent, node *intent.NodeSpec) (string, error) {
	data, err := LoadTemplate(templateDir, i.Network)
	if err != nil {
		return "", err
	}

	config := string(data)

	// Apply port overrides
	config = applyPortOverrides(config, node)

	// Apply feature overrides
	config = applyFeatureOverrides(config, node)

	return config, nil
}

// applyPortOverrides patches port settings in the HOCON config.
func applyPortOverrides(config string, node *intent.NodeSpec) string {
	ports := node.Ports

	if ports.HTTP != 0 {
		config = replaceHOCONValue(config, "fullNodePort", fmt.Sprintf("%d", ports.HTTP))
	}
	if ports.GRPC != 0 {
		config = replaceRPCPort(config, ports.GRPC)
	}
	if ports.SolidityHTTP != 0 {
		config = replaceHOCONValue(config, "solidityPort", fmt.Sprintf("%d", ports.SolidityHTTP))
	}
	if ports.P2P != 0 {
		config = replaceListenPort(config, ports.P2P)
	}

	return config
}

// applyFeatureOverrides enables/disables features in the HOCON config.
func applyFeatureOverrides(config string, node *intent.NodeSpec) string {
	features := node.Features

	if features.JSONRPC != nil && *features.JSONRPC {
		// Ensure jsonrpc block has httpFullNodeEnable = true
		config = ensureJSONRPCEnabled(config)
	}

	return config
}

// replaceHOCONValue replaces a simple key = value pattern in HOCON.
func replaceHOCONValue(config, key, newValue string) string {
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+" =") || strings.HasPrefix(trimmed, key+"=") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%s%s = %s", indent, key, newValue)
			break
		}
	}
	return strings.Join(lines, "\n")
}

// replaceRPCPort replaces the gRPC port in the rpc block.
func replaceRPCPort(config string, port int) string {
	lines := strings.Split(config, "\n")
	inRPC := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "rpc {" || strings.HasPrefix(trimmed, "rpc {") {
			inRPC = true
			continue
		}
		if inRPC && strings.HasPrefix(trimmed, "port") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%sport = %d", indent, port)
			break
		}
		if inRPC && trimmed == "}" {
			inRPC = false
		}
	}
	return strings.Join(lines, "\n")
}

// replaceListenPort replaces the P2P listen port.
func replaceListenPort(config string, port int) string {
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "listen.port") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%slisten.port = %d", indent, port)
			break
		}
	}
	return strings.Join(lines, "\n")
}

// ensureJSONRPCEnabled ensures the jsonrpc block has httpFullNodeEnable = true.
func ensureJSONRPCEnabled(config string) string {
	lines := strings.Split(config, "\n")
	inJSONRPC := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "jsonrpc") && strings.Contains(trimmed, "{") {
			inJSONRPC = true
			continue
		}
		if inJSONRPC {
			if strings.Contains(trimmed, "httpFullNodeEnable") {
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[i] = fmt.Sprintf("%shttpFullNodeEnable = true", indent)
				return strings.Join(lines, "\n")
			}
			if trimmed == "}" {
				// Insert before closing brace
				indent := "    "
				lines[i] = indent + "httpFullNodeEnable = true\n" + line
				return strings.Join(lines, "\n")
			}
		}
	}
	return config
}
