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

	// String returns a human-readable description of the target.
	String() string
}
