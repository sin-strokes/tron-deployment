package security

import (
	"fmt"
	"strings"
)

// allowedCommands is the whitelist of commands that trond may execute on remote targets.
// Any command not in this list is rejected to prevent shell injection via intent fields.
var allowedCommands = map[string]bool{
	// Docker
	"docker": true,
	// Systemd
	"systemctl":  true,
	"journalctl": true,
	// File operations
	"cat":   true,
	"mkdir": true,
	"chmod": true,
	"chown": true,
	"rm":    true,
	"ls":    true,
	"mv":    true,
	"cp":    true,
	"tee":   true,
	// System info
	"df":        true,
	"free":      true,
	"grep":      true,
	"uname":     true,
	"whoami":    true,
	"id":        true,
	"which":     true,
	"java":      true,
	"curl":      true,
	"wget":      true,
	"sha256sum": true,
	// Process management
	"kill":  true,
	"pkill": true,
	"pgrep": true,
	"ps":    true,
	// Network
	"ss":      true,
	"netstat": true,
	// Package managers (for bootstrap)
	"apt-get": true,
	"yum":     true,
	"dnf":     true,
}

// ValidateCommand checks if a command is in the SSH whitelist.
// Returns an error if the command is not allowed.
func ValidateCommand(cmd string) error {
	base := extractBaseCommand(cmd)
	if !allowedCommands[base] {
		return fmt.Errorf("command %q is not in the SSH whitelist; allowed commands: %s",
			base, allowedCommandList())
	}
	return nil
}

// extractBaseCommand gets the first word (the command name) from a command string.
func extractBaseCommand(cmd string) string {
	// Handle sudo prefix
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}
	base := parts[0]
	if base == "sudo" && len(parts) > 1 {
		base = parts[1]
	}
	return base
}

func allowedCommandList() string {
	cmds := make([]string, 0, len(allowedCommands))
	for cmd := range allowedCommands {
		cmds = append(cmds, cmd)
	}
	return strings.Join(cmds, ", ")
}
