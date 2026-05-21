//go:build !windows

package build

import (
	"errors"
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
	return acquireLockHelper(cacheDir, key, syscall.LOCK_EX)
}

// TryAcquireCacheLock is the non-blocking variant: returns
// (nil, false, nil) if another process currently holds the lock,
// (release, true, nil) on success, or (nil, false, err) on a
// genuine error opening the lock file. Used by Prune so a periodic
// cache cleanup never blocks waiting for an active build to finish
// — it just skips that entry and moves on.
func TryAcquireCacheLock(cacheDir, key string) (release func(), ok bool, err error) {
	rel, err := acquireLockHelper(cacheDir, key, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// EWOULDBLOCK / EAGAIN = "lock held by someone else" — the
		// caller treats this as "skip", not "fail".
		if errno, isErrno := lockErrno(err); isErrno &&
			(errno == syscall.EWOULDBLOCK || errno == syscall.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return rel, true, nil
}

// lockErrno walks the wrapped error chain looking for a syscall.Errno.
// fmt.Errorf("flock: %w", err) wraps the raw errno so a simple
// errors.As suffices; the explicit helper keeps the call site
// readable.
func lockErrno(err error) (syscall.Errno, bool) {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno, true
	}
	return 0, false
}

func acquireLockHelper(cacheDir, key string, flockFlag int) (func(), error) {
	if err := os.MkdirAll(filepath.Join(cacheDir, "locks"), 0o700); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}
	lockPath := filepath.Join(cacheDir, "locks", key+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), flockFlag); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return func() {
		// Best-effort release on the way out. Unlock errors are
		// recoverable noise (the kernel drops the lock when the fd
		// closes anyway).
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
