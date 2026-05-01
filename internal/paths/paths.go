// Package paths centralises trond's filesystem layout. State, audit log and
// per-deployment work directories all derive from a single base directory so
// test harnesses can isolate concurrent runs by overriding the base.
//
// Resolution order:
//
//  1. The value passed to SetBaseDir (e.g. from --state-dir).
//  2. The TROND_STATE_DIR environment variable.
//  3. ~/.trond.
//
// Resolution happens at call time (not at process start), so callers can
// adjust SetBaseDir before any path is resolved.
package paths

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	mu       sync.RWMutex
	override string // set via SetBaseDir
)

// SetBaseDir overrides the base directory used to resolve subsequent paths.
// Pass an empty string to clear the override (env / default will apply).
func SetBaseDir(dir string) {
	mu.Lock()
	defer mu.Unlock()
	override = dir
}

// BaseDir returns the resolved base directory.
func BaseDir() string {
	mu.RLock()
	o := override
	mu.RUnlock()
	if o != "" {
		return o
	}
	if env := os.Getenv("TROND_STATE_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// HOME unset (some CI containers): fall back to current dir.
		return ".trond"
	}
	return filepath.Join(home, ".trond")
}

// State returns the path of state.json.
func State() string {
	return filepath.Join(BaseDir(), "state.json")
}

// AuditLog returns the path of audit.log.
func AuditLog() string {
	return filepath.Join(BaseDir(), "audit.log")
}

// Deployments returns the directory holding per-node compose / config files.
func Deployments() string {
	return filepath.Join(BaseDir(), "deployments")
}

// SnapshotJobs returns the directory where detached `snapshot download`
// jobs persist their manifest (.json) and combined stdout/stderr log.
// Created on demand by the caller.
func SnapshotJobs() string {
	return filepath.Join(BaseDir(), "snapshots")
}
