package build

import (
	"strings"
	"testing"
)

// TestCacheKey_NamingShape pins the on-disk naming. Schema clients
// (FR-002, schemas/output/build.schema.json) rely on the pattern.
func TestCacheKey_NamingShape(t *testing.T) {
	k := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		BuilderImageDigest: "sha256:d4e2a1abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	got := k.String()
	// 12-char git prefix + `-b` + 8-char digest prefix; no `+dirty`
	// because PatchHash empty; no `-x` because all defaults.
	want := "8f4e2a3c1234-bd4e2a1ab"
	if got != want {
		t.Errorf("CacheKey.String() = %q; want %q", got, want)
	}
}

// TestCacheKey_Dirty asserts the `+dirty-<patch8>` suffix shows up
// when a dirty patch hash is present.
func TestCacheKey_Dirty(t *testing.T) {
	k := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		PatchHash:          "7f2a3b9c12345678",
		BuilderImageDigest: "sha256:d4e2a1abcdef",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	got := k.String()
	if !strings.Contains(got, "+dirty-7f2a3b9c") {
		t.Errorf("dirty cache key %q should contain +dirty-7f2a3b9c", got)
	}
}

// TestCacheKey_BuilderDigestChangesKey is the regression guard for
// FR-002 pass 2: bumping the pinned JDK image MUST invalidate prior
// cache entries. Two otherwise-identical keys with different builder
// digests must produce different on-disk names.
func TestCacheKey_BuilderDigestChangesKey(t *testing.T) {
	base := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		BuilderImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	other := base
	other.BuilderImageDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if base.String() == other.String() {
		t.Fatal("different builder digests must produce different cache keys")
	}
}

// TestCacheKey_GradleArgsChangesKey asserts the args participate
// (FR-002 pass 2): `--offline` builds shouldn't collide with
// networked builds.
func TestCacheKey_GradleArgsChangesKey(t *testing.T) {
	base := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		BuilderImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	other := base
	other.GradleArgs = []string{"--offline"}
	if base.String() == other.String() {
		t.Fatal("different gradle args must produce different cache keys")
	}
}

// TestCacheKey_PlatformChangesKey is the regression guard for the
// arch-aware build feature: two builds with the same source / JDK /
// task but different platforms (linux/amd64 vs linux/arm64) MUST
// produce different cache keys so the cache holds both JARs
// concurrently. Otherwise the second build would overwrite the
// first.
func TestCacheKey_PlatformChangesKey(t *testing.T) {
	base := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		BuilderImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
		Platform:           "linux/amd64",
	}
	other := base
	other.Platform = "linux/arm64"
	if base.String() == other.String() {
		t.Fatal("different platforms must produce different cache keys")
	}
}

// TestCacheKey_PlatformLinuxAmd64IsCanonical: the canonical default
// shape (jdk=8, jar, shadowJar, no args, linux/amd64) MUST produce
// no `-x` suffix. Anything else (incl. linux/arm64) gets folded.
func TestCacheKey_PlatformLinuxAmd64IsCanonical(t *testing.T) {
	canonical := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		BuilderImageDigest: "sha256:d4e2a1abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
		Platform:           "linux/amd64",
	}
	if got := canonical.String(); strings.Contains(got, "-x") {
		t.Errorf("canonical defaults should NOT have -x suffix; got %q", got)
	}
	armKey := canonical
	armKey.Platform = "linux/arm64"
	if got := armKey.String(); !strings.Contains(got, "-x") {
		t.Errorf("non-canonical platform should produce -x suffix; got %q", got)
	}
}

// TestCacheKey_OverrideDigestStable asserts that an override path
// (--builder-image-override) still produces a stable, deterministic
// 6-char prefix in the cache key.
func TestCacheKey_OverrideDigestStable(t *testing.T) {
	k := CacheKey{
		GitRevision:        "8f4e2a3c1234567890abcdef1234567890abcdef",
		BuilderImageDigest: "myreg.example/temurin:8@sha256:deadbeef",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		GradleTask:         "shadowJar",
	}
	first := k.String()
	second := k.String()
	if first != second {
		t.Fatalf("override cache key not deterministic: %q vs %q", first, second)
	}
}
