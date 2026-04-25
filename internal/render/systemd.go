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

	// Build ExecStart. Standard order: java [jvm args] -jar <jar> -c <conf>
	// [extra args] [--witness]
	execParts := []string{
		"/usr/bin/java",
		jvmArgs,
		"-jar", jarPath,
		"-c", configPath,
	}
	execParts = append(execParts, node.ExtraArgs...)
	if node.Type == "witness" {
		execParts = append(execParts, "--witness")
	}
	execStart := strings.Join(execParts, " ")

	// Build environment lines. extra_env is honored on systemd nodes too.
	// Witness key env name is preserved (handled by trond runtime via a
	// drop-in override file, not embedded here).
	envLines := make([]string, 0, len(node.ExtraEnv))
	for _, k := range sortedKeys(node.ExtraEnv) {
		envLines = append(envLines, fmt.Sprintf("Environment=%s=%s", k, node.ExtraEnv[k]))
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
	// Map docker-compose restart names to systemd. unless-stopped maps to
	// "always" because systemd has no built-in equivalent that preserves
	// "stop" intent across reboots; users who really mean unless-stopped
	// should rely on `systemctl stop` keeping the unit disabled.
	restart := "on-failure"
	switch node.Restart {
	case "no":
		restart = "no"
	case "always", "unless-stopped":
		restart = "always"
	case "on-failure", "":
		restart = "on-failure"
	}
	sb.WriteString(fmt.Sprintf("Restart=%s\n", restart))
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
