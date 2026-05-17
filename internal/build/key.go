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
	GitRevision        string   // full sha
	PatchHash          string   // sha256 hex if dirty, else ""
	BuilderImageDigest string   // "sha256:abc..." or override ref
	JDKVersion         string
	ArtifactKind       string // "jar" | "image"
	GradleTask         string
	GradleArgs         []string // already validated by ValidateGradleArgs
}

// String produces the on-disk cache key:
//
//	<sha>-b<digest6>[+dirty-<patch8>]
//
// digest6 is the first 6 hex chars of BuilderImageDigest's sha256
// portion (or, for overrides, of the sha256 of the override string).
//
// Examples:
//
//	8f4e2a3c...-bd4e2a1
//	8f4e2a3c...-bd4e2a1+dirty-7f2a3b9c
func (k CacheKey) String() string {
	if k.GitRevision == "" {
		// Cache key invariant; let the caller surface the error.
		return "INVALID"
	}
	d := k.digestPrefix()
	base := fmt.Sprintf("%s-b%s", k.GitRevision[:short(k.GitRevision, 7)], d)
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

// extraFold returns "" when all build-shape inputs are the natural
// default (jdk=8, artifact=jar, gradle_task=shadowJar, no args).
// Otherwise it returns a short hash so different shapes don't collide.
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
	args := append([]string(nil), k.GradleArgs...)
	sort.Strings(args)
	if jdk == "8" && kind == "jar" && task == "shadowJar" && len(args) == 0 {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "jdk=%s\nkind=%s\ntask=%s\nargs=%s\n",
		jdk, kind, task, strings.Join(args, "\x00"))
	return hex.EncodeToString(h.Sum(nil))[:6]
}

// digestPrefix extracts the 6-hex-char "build identity" portion of
// BuilderImageDigest. For canonical pinned digests
// (`sha256:abc...`) it's the prefix of the sha. For overrides (an
// arbitrary ref@digest string) we hash the whole string so the cache
// key still differs from any pinned build.
func (k CacheKey) digestPrefix() string {
	d := k.BuilderImageDigest
	if strings.HasPrefix(d, "sha256:") {
		return d[7:short(d, 13)]
	}
	// Override path: hash the whole thing for stable prefixing.
	h := sha256.Sum256([]byte(d))
	return hex.EncodeToString(h[:])[:6]
}

func short(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
