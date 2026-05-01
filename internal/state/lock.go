package state

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock provides flock-based file locking for the state file.
type Lock struct {
	path string
	file *os.File
}

// NewLock creates a lock manager for the given state directory.
func NewLock(stateDir string) *Lock {
	return &Lock{
		path: filepath.Join(stateDir, "state.lock"),
	}
}

// Acquire takes an exclusive lock. Blocks if another process holds it.
func (l *Lock) Acquire() error {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return fmt.Errorf("acquire lock: %w", err)
	}

	l.file = f
	return nil
}

// TryAcquire attempts to take the lock without blocking.
// Returns false if already held by another process.
func (l *Lock) TryAcquire() (bool, error) {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false, fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, fmt.Errorf("open lock file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return false, nil // Lock held by another process
	}

	l.file = f
	return true, nil
}

// Release releases the lock.
func (l *Lock) Release() error {
	if l.file == nil {
		return nil
	}

	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	l.file = nil
	return err
}
