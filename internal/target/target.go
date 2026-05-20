package target

import (
	"context"
	"os"
)

// Target abstracts command execution and file operations on a deployment target.
type Target interface {
	// Exec runs a command on the target and returns combined output.
	Exec(ctx context.Context, cmd string, args ...string) ([]byte, error)

	// Upload copies a local file to the target.
	Upload(ctx context.Context, localPath, remotePath string) error

	// Download copies a file from the target to local.
	Download(ctx context.Context, remotePath, localPath string) error

	// ReadFile reads a file from the target.
	ReadFile(ctx context.Context, path string) ([]byte, error)

	// WriteFile writes data to a file on the target.
	WriteFile(ctx context.Context, path string, data []byte, perm os.FileMode) error

	// DiskFree returns available disk space in bytes at the given path.
	DiskFree(ctx context.Context, path string) (uint64, error)

	// MemTotal returns total system memory in bytes.
	MemTotal(ctx context.Context) (uint64, error)

	// PutFile streams a local file to a remote path with atomic
	// install semantics (data lands at <remotePath>.tmp first, then
	// mv renames). Used by the Phase 4 build pipeline to transfer a
	// locally-built JAR to an SSH target. LocalTarget's
	// implementation is a same-fs copy (or no-op when localPath ==
	// remotePath).
	PutFile(ctx context.Context, localPath, remotePath string) error

	// Sha256IfExists returns the hex sha256 of a file at the given
	// path, or empty string if the file does not exist. Used to skip
	// transfer when the target already holds the bit-identical
	// artifact.
	Sha256IfExists(ctx context.Context, path string) (string, error)

	// CommandExists reports whether the named executable resolves
	// on the target's PATH. Used by preflight to fail-fast before
	// apply if a required tool (scp, sha256sum, ...) is missing.
	CommandExists(ctx context.Context, name string) bool

	// String returns a human-readable description of the target.
	String() string
}
