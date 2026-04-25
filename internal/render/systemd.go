package render

import (
	"fmt"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/intent"
)

// RenderSystemdUnit generates a systemd service unit file from the intent.
func RenderSystemdUnit(i *intent.Intent, node *intent.NodeSpec, jvmArgs string, jarPath string, configPath string) string {
	user := node.SystemUser
	if user == "" {
		user = "tron"
	}

	installPath := node.InstallPath
	if installPath == "" {
		installPath = "/opt/tron"
	}

	if jarPath == "" {
		jarPath = fmt.Sprintf("%s/FullNode.jar", installPath)
	}
	if configPath == "" {
		configPath = fmt.Sprintf("%s/config.conf", installPath)
	}

	// Build ExecStart
	execParts := []string{
		"/usr/bin/java",
		jvmArgs,
		"-jar", jarPath,
		"-c", configPath,
	}
	if node.Type == "witness" {
		execParts = append(execParts, "--witness")
	}
	execStart := strings.Join(execParts, " ")

	// Build environment lines
	var envLines []string
	if node.Type == "witness" && node.WitnessKeyEnv != "" {
		envLines = append(envLines, fmt.Sprintf("Environment=%s=%%s", node.WitnessKeyEnv))
	}

	memory := node.Resources.Memory
	if memory == "" {
		memory = "16G"
	}
	// Normalize: "16GB" → "16G"
	memoryMax := strings.TrimSuffix(memory, "B")

	unitName := fmt.Sprintf("tron-%s", i.Name)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Managed by trond — do not edit manually\n"))
	sb.WriteString(fmt.Sprintf("# Node: %s | Network: %s\n", i.Name, i.Network))
	sb.WriteString("[Unit]\n")
	sb.WriteString(fmt.Sprintf("Description=TRON %s Node (%s)\n", strings.Title(node.Type), i.Name))
	sb.WriteString("After=network-online.target\n")
	sb.WriteString("Wants=network-online.target\n")
	sb.WriteString("\n")
	sb.WriteString("[Service]\n")
	sb.WriteString("Type=simple\n")
	sb.WriteString(fmt.Sprintf("User=%s\n", user))
	sb.WriteString(fmt.Sprintf("WorkingDirectory=%s\n", installPath))
	sb.WriteString(fmt.Sprintf("ExecStart=%s\n", execStart))
	for _, env := range envLines {
		sb.WriteString(fmt.Sprintf("%s\n", env))
	}
	sb.WriteString("Restart=on-failure\n")
	sb.WriteString("RestartSec=10\n")
	sb.WriteString(fmt.Sprintf("MemoryMax=%s\n", memoryMax))
	sb.WriteString("LimitNOFILE=65536\n")
	sb.WriteString("StandardOutput=journal\n")
	sb.WriteString("StandardError=journal\n")
	sb.WriteString(fmt.Sprintf("SyslogIdentifier=%s\n", unitName))
	sb.WriteString("\n")
	sb.WriteString("[Install]\n")
	sb.WriteString("WantedBy=multi-user.target\n")

	return sb.String()
}
