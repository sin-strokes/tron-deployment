package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tronprotocol/tron-deployment/internal/paths"
)

// CacheDir returns the root of the build cache:
// `${TROND_STATE_DIR}/builds`. Created lazily by callers via
// EnsureCacheDirs.
func CacheDir() string {
	return filepath.Join(paths.BaseDir(), "builds")
}

// EnsureCacheDirs creates the cache subdirectories. Idempotent.
func EnsureCacheDirs() error {
	for _, sub := range []string{"out", "images", "manifest", "locks", "gradle"} {
		p := filepath.Join(CacheDir(), sub)
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
	}
	return nil
}

// manifestPath returns the manifest path for a cache key.
func manifestPath(key string) string {
	return filepath.Join(CacheDir(), "manifest", key+".json")
}

// Lookup checks whether a build matching key already exists and is
// still usable. Per FR-020 the manifest file's existence is necessary
// but not sufficient — we MUST also stat the artifact (jar or image
// metadata) that the manifest points at. A user who manually deleted
// a JAR shouldn't get a stale cache hit.
func Lookup(key CacheKey) (*CacheHit, error) {
	mp := manifestPath(key.String())
	m, err := readManifest(mp)
	if errors.Is(err, os.ErrNotExist) {
		return &CacheHit{Hit: false}, nil
	}
	if err != nil {
		return nil, err
	}
	// Verify the referenced artifact actually exists on disk (FR-020).
	switch m.ArtifactKind {
	case "jar":
		if _, statErr := os.Stat(m.ArtifactPath); errors.Is(statErr, os.ErrNotExist) {
			// Drop the orphan manifest. Next build re-creates everything.
			_ = os.Remove(mp)
			return &CacheHit{Hit: false}, nil
		} else if statErr != nil {
			return nil, fmt.Errorf("stat cached artifact: %w", statErr)
		}
	case "image":
		// Image artifacts are tracked via images/<key>.json. Phase 3
		// fills this in; for Phase 1 we treat missing as miss.
		if _, statErr := os.Stat(filepath.Join(CacheDir(), "images", key.String()+".json")); errors.Is(statErr, os.ErrNotExist) {
			_ = os.Remove(mp)
			return &CacheHit{Hit: false}, nil
		}
	}
	return &CacheHit{Hit: true, Manifest: m}, nil
}

// Save persists a manifest to manifest/<key>.json. Caller is
// responsible for atomicity vs. concurrent build callers — FR-015's
// flock already serializes around the same cache key.
func Save(m *Manifest) error {
	return writeManifest(manifestPath(m.CacheKey), m)
}
