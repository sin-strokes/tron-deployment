package cmd

import (
	"github.com/tronprotocol/tron-deployment/internal/paths"
)

// stateDir, statePath, auditLogPath, deploymentsDir are thin wrappers around
// the internal/paths package. Subpackages (cmd/network, cmd/config) import
// internal/paths directly so they don't need to round-trip through cmd.
func stateDir() string       { return paths.BaseDir() }
func statePath() string      { return paths.State() }
func auditLogPath() string   { return paths.AuditLog() }
func deploymentsDir() string { return paths.Deployments() }
