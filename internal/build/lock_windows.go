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
	windowsCacheMu.Lock()
	m, ok := windowsCacheMu.keys[key]
	if !ok {
		m = &sync.Mutex{}
		windowsCacheMu.keys[key] = m
	}
	windowsCacheMu.Unlock()

	m.Lock()
	return func() {
		m.Unlock()
	}, nil
}
