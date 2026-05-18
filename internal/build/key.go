package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// CacheKey is content-addressed by every input that can change the
// build output. The on-disk name comes from String(); FR-002 pins
// the format.
//
// Including BuilderImageDigest (FR-002 pass 2) means a trond release
// that bumps the pinned JDK image automatically invalidates every
// cached artifact — no manual `build prune` needed. Including
// GradleArgs (also pass 2) means `--offline` builds don't collide
// with networked builds.
type CacheKey struct {
	GitRevision        string // full sha
	PatchHash          string // sha256 hex if dirty, else ""
	BuilderImageDigest string // "sha256:abc..." or override ref
	JDKVersion         string
	ArtifactKind       string // "jar" | "image"
	GradleTask         string
	GradleArgs         []string // already validated by ValidateGradleArgs
	Platform           string   // docker --platform e.g. "linux/arm64"; empty = host default
}

// String produces the on-disk cache key:
//
//	<git-sha-12>-b<digest-8>[+dirty-<patch-8>][-x<extra-8>]
//
// Lengths are sized so cosmic-ray collisions stay implausible across
// a single user's cache (rarely > 10k entries):
//
//   - 12 hex git prefix → 48 bits, matches git's --abbrev=12 default
//   - 8 hex digest prefix → 32 bits; bumping a pin gives a distinct key
//   - 8 hex patch prefix → 32 bits; per-dirty-edit variant key
//   - 8 hex extra prefix → 32 bits; folds non-default JDK/task/args
//
// Examples:
//
//	8f4e2a3c1234-bd4e2a1c
//	8f4e2a3c1234-bd4e2a1c+dirty-7f2a3b9c
//	8f4e2a3c1234-bd4e2a1c-xa1b2c3d4
func (k CacheKey) String() string {
	if k.GitRevision == "" {
		// Cache key invariant; let the caller surface the error.
		return "INVALID"
	}
	d := k.digestPrefix()
	base := fmt.Sprintf("%s-b%s", k.GitRevision[:short(k.GitRevision, 12)], d)
	if k.PatchHash != "" {
		base = fmt.Sprintf("%s+dirty-%s", base, k.PatchHash[:short(k.PatchHash, 8)])
	}
	// Fold ArtifactKind / JDKVersion / GradleTask / GradleArgs into a
	// content hash appended only when one of them differs from the
	// natural default. Avoids cluttering the typical case.
	if extra := k.extraFold(); extra != "" {
		base = fmt.Sprintf("%s-x%s", base, extra)
	}
	return base
}

// extraFold returns "" when all build-shape inputs match the
// canonical default profile (jdk=8, artifact=jar,
// gradle_task=shadowJar, platform=linux/amd64, no args). Otherwise
// returns a short hash so different shapes don't collide.
//
// The "canonical default" is fixed at JDK 8 + linux/amd64 so the
// cache key is host-independent: an amd64 host and an arm64 host
// running otherwise-identical commands produce different keys
// because their platform differs, not because of a host-specific
// notion of "default".
func (k CacheKey) extraFold() string {
	jdk := k.JDKVersion
	if jdk == "" {
		jdk = "8"
	}
	kind := k.ArtifactKind
	if kind == "" {
		kind = "jar"
	}
	task := k.GradleTask
	if task == "" {
		switch kind {
		case "jar":
			task = "shadowJar"
		case "image":
			task = "dockerBuild"
		}
	}
	platform := k.Platform
	if platform == "" {
		platform = "linux/amd64"
	}
	args := append([]string(nil), k.GradleArgs...)
	sort.Strings(args)
	if jdk == "8" && kind == "jar" && task == "shadowJar" &&
		platform == "linux/amd64" && len(args) == 0 {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "jdk=%s\nkind=%s\ntask=%s\nplatform=%s\nargs=%s\n",
		jdk, kind, task, platform, strings.Join(args, "\x00"))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// digestPrefix extracts the 8-hex-char "build identity" portion of
// BuilderImageDigest. For canonical pinned digests
// (`sha256:abc...`) it's the prefix of the sha. For overrides (an
// arbitrary ref@digest string) we hash the whole string so the cache
// key still differs from any pinned build.
func (k CacheKey) digestPrefix() string {
	d := k.BuilderImageDigest
	if strings.HasPrefix(d, "sha256:") {
		return d[7:short(d, 15)]
	}
	// Override path: hash the whole thing for stable prefixing.
	h := sha256.Sum256([]byte(d))
	return hex.EncodeToString(h[:])[:8]
}

func short(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
