package pins

import (
	"strings"
	"testing"
)

// TestResolve_PinnedHit asserts a known JDK + platform resolves to a
// canonical `<ref>@<digest>` and reports the digest separately for
// cache-key inclusion (FR-002).
func TestResolve_PinnedHit(t *testing.T) {
	ref, digest, ok := Resolve("8", "linux/amd64", "")
	if !ok {
		t.Fatal("expected JDK 8 + linux/amd64 pin to exist; got miss")
	}
	if !strings.Contains(ref, "@sha256:") {
		t.Errorf("ref %q must include @sha256: portion (canonical form)", ref)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest %q must start with sha256:", digest)
	}
}

// TestResolve_PerPlatformDigests asserts the same JDK pin returns
// DIFFERENT digests for different platforms — this is the whole
// point of the per-platform schema. Same digest for both would
// mean we accidentally stored the manifest-list digest, which
// would re-introduce the `docker run --platform X image@<list>`
// "cannot overwrite digest" bug.
func TestResolve_PerPlatformDigests(t *testing.T) {
	_, amdDigest, amdOK := Resolve("8", "linux/amd64", "")
	_, armDigest, armOK := Resolve("8", "linux/arm64", "")
	if !amdOK || !armOK {
		t.Skip("pin file missing one platform variant; skip")
	}
	if amdDigest == armDigest {
		t.Errorf("amd64 and arm64 digests must differ; both = %q", amdDigest)
	}
}

// TestResolve_UnknownJDK asserts an unsupported JDK version reports
// a miss rather than a fallback.
func TestResolve_UnknownJDK(t *testing.T) {
	_, _, ok := Resolve("99", "linux/amd64", "")
	if ok {
		t.Error("expected JDK 99 to be unknown; got hit")
	}
}

// TestResolve_UnknownPlatform asserts a known JDK + unknown platform
// is also a miss — without this, future platform additions
// (windows/amd64, etc.) wouldn't surface as a clear validation error.
func TestResolve_UnknownPlatform(t *testing.T) {
	_, _, ok := Resolve("8", "windows/amd64", "")
	if ok {
		t.Error("expected unknown platform to miss; got hit")
	}
}

// TestResolve_Override threads --builder-image-override through. The
// returned digest must equal the override itself (FR-024) so changes
// in override participate in the cache key.
func TestResolve_Override(t *testing.T) {
	override := "myregistry.example/temurin:8-jdk@sha256:" + strings.Repeat("a", 64)
	ref, digest, ok := Resolve("8", "linux/amd64", override)
	if !ok {
		t.Fatal("expected override to be accepted")
	}
	if ref != override {
		t.Errorf("ref = %q; want override %q", ref, override)
	}
	if digest != override {
		t.Errorf("override should be reported as the cache digest; got %q", digest)
	}
}

// TestVersions surfaces the discoverable pin set for preflight /
// error messages.
func TestVersions(t *testing.T) {
	got := Versions()
	if len(got) == 0 {
		t.Fatal("Versions() returned empty")
	}
	wantAny := []string{"8", "11", "17", "21"}
	hit := false
	for _, w := range wantAny {
		for _, g := range got {
			if g == w {
				hit = true
			}
		}
	}
	if !hit {
		t.Errorf("expected at least one of %v in pin versions; got %v", wantAny, got)
	}
}

// TestPlatforms returns the supported platforms for a JDK version.
// Both linux/amd64 and linux/arm64 must be present (java-tron's
// compat matrix relies on the pair).
func TestPlatforms(t *testing.T) {
	got := Platforms("8")
	want := map[string]bool{"linux/amd64": true, "linux/arm64": true}
	for _, p := range got {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing platforms: %v (got %v)", want, got)
	}
}
