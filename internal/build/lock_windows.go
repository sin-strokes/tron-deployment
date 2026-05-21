//go:build windows

package build

import "sync"

// AcquireCacheLock on Windows falls back to an in-process mutex only.
// `syscall.Flock` is POSIX-specific; trond's docker-builder path is
// already de-facto Unix-only (`docker.sock`, gradle wrapper assumes
// bash). Cross-process serialization on Windows is undefined (FR-015
// caveat) and documented in AGENTS.md.
//
// Two trond processes on the same Windows host racing the same cache
// key may both build; the cache key + content-addressed naming means
// they produce identical artifacts (the loser wastes CPU). Not a
// correctness bug; just inefficient.
var windowsCacheMu = struct {
	sync.Mutex
	keys map[string]*sync.Mutex
}{keys: map[string]*sync.Mutex{}}

func AcquireCacheLock(_, key string) (release func(), err error) {
	m := getKeyMutex(key)
	m.Lock()
	return func() {
		m.Unlock()
	}, nil
}

// TryAcquireCacheLock is the non-blocking variant. Mirrors the posix
// path's contract: returns (release, true, nil) on success,
// (nil, false, nil) when the lock is already held in-process.
func TryAcquireCacheLock(_, key string) (release func(), ok bool, err error) {
	m := getKeyMutex(key)
	if !m.TryLock() {
		return nil, false, nil
	}
	return func() { m.Unlock() }, true, nil
}

func getKeyMutex(key string) *sync.Mutex {
	windowsCacheMu.Lock()
	defer windowsCacheMu.Unlock()
	m, ok := windowsCacheMu.keys[key]
	if !ok {
		m = &sync.Mutex{}
		windowsCacheMu.keys[key] = m
	}
	return m
}
