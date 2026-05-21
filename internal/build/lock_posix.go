//go:build !windows

package build

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireCacheLock serializes concurrent `trond build` invocations
// against the same cache key (FR-015). The flock is held for the
// caller's lifetime; release() drops it.
//
// Posix path: an exclusive lock on a file under
// `<cacheDir>/locks/<key>.lock`. Other processes calling
// AcquireCacheLock with the same key block until we Release.
func AcquireCacheLock(cacheDir, key string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Join(cacheDir, "locks"), 0o700); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}
	lockPath := filepath.Join(cacheDir, "locks", key+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock LOCK_EX: %w", err)
	}
	return func() {
		// Best-effort release on the way out. Unlock errors are
		// recoverable noise (the kernel drops the lock when the fd
		// closes anyway).
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
