package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/paths"
)

func withTempBaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paths.SetBaseDir(dir)
	t.Cleanup(func() { paths.SetBaseDir("") })
	return dir
}

func TestEnsureCacheDirs(t *testing.T) {
	base := withTempBaseDir(t)
	if err := EnsureCacheDirs(); err != nil {
		t.Fatalf("EnsureCacheDirs: %v", err)
	}
	for _, sub := range []string{"out", "images", "manifest", "locks", "gradle"} {
		if _, err := os.Stat(filepath.Join(base, "builds", sub)); err != nil {
			t.Errorf("expected %s/builds/%s to exist: %v", base, sub, err)
		}
	}
}

func TestLookup_NoManifest(t *testing.T) {
	withTempBaseDir(t)
	if err := EnsureCacheDirs(); err != nil {
		t.Fatalf("EnsureCacheDirs: %v", err)
	}
	hit, err := Lookup(context.Background(), CacheKey{
		GitRevision:        "abc123def456789012345678901234567890abcd",
		BuilderImageDigest: "sha256:aaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if hit.Hit {
		t.Error("empty cache should report Hit=false")
	}
}

// TestLookup_StatsArtifact is the FR-020 regression guard: a manifest
// pointing at a missing JAR MUST be treated as a miss, and the
// orphan manifest MUST be removed.
func TestLookup_StatsArtifact(t *testing.T) {
	withTempBaseDir(t)
	if err := EnsureCacheDirs(); err != nil {
		t.Fatalf("EnsureCacheDirs: %v", err)
	}

	key := CacheKey{
		GitRevision:        "abc123def456789012345678901234567890abcd",
		BuilderImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	// Write a manifest pointing at a non-existent file.
	manifest := &Manifest{
		CacheKey:     key.String(),
		ArtifactKind: "jar",
		ArtifactPath: "/definitely/does/not/exist.jar",
		CreatedAt:    time.Now().UTC(),
	}
	if err := Save(manifest); err != nil {
		t.Fatalf("Save: %v", err)
	}

	hit, err := Lookup(context.Background(), key)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if hit.Hit {
		t.Fatal("missing artifact should produce a cache miss (FR-020)")
	}

	// Manifest should be removed by the orphan cleanup.
	mp := filepath.Join(CacheDir(), "manifest", key.String()+".json")
	if _, err := os.Stat(mp); !os.IsNotExist(err) {
		t.Errorf("orphan manifest should have been removed; stat err=%v", err)
	}
}

func TestLookup_HitWhenArtifactPresent(t *testing.T) {
	base := withTempBaseDir(t)
	if err := EnsureCacheDirs(); err != nil {
		t.Fatalf("EnsureCacheDirs: %v", err)
	}

	key := CacheKey{
		GitRevision:        "abc123def456789012345678901234567890abcd",
		BuilderImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	artifactPath := filepath.Join(base, "builds", "out", key.String()+".jar")
	if err := os.WriteFile(artifactPath, []byte("not a real jar"), 0o600); err != nil {
		t.Fatalf("plant artifact: %v", err)
	}

	manifest := &Manifest{
		CacheKey:     key.String(),
		ArtifactKind: "jar",
		ArtifactPath: artifactPath,
		CreatedAt:    time.Now().UTC(),
	}
	if err := Save(manifest); err != nil {
		t.Fatalf("Save: %v", err)
	}

	hit, err := Lookup(context.Background(), key)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !hit.Hit {
		t.Fatal("manifest + artifact both present should be Hit=true")
	}
	if hit.Manifest.CacheKey != key.String() {
		t.Errorf("hit manifest cache key mismatch: %q vs %q",
			hit.Manifest.CacheKey, key.String())
	}
}
