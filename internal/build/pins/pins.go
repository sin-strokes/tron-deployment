// Package pins holds the trond release's pinned set of builder image
// digests, embedded into the binary so a deployed trond is the source
// of truth (no out-of-band JSON file the user could de-sync).
//
// Bump policy: a Makefile target `refresh-builder-pins` re-resolves
// Eclipse Temurin tags to current digests and rewrites the JSON. The
// regeneration happens at trond release-prep time, not at runtime.
//
// Schema: per-platform digests, not manifest-list digests.
// Eclipse Temurin's `:8-jdk-jammy` is a multi-arch manifest list;
// `docker pull` on a tag resolves to the host's arch variant
// automatically. But trond needs to support cross-arch
// (`--platform linux/amd64` on an arm64 host), and docker rejects
// `docker run --platform X image@<manifest-list-digest>` with
// "cannot overwrite digest" — the manifest list digest doesn't
// match the per-arch image we'd actually run. So we pin the
// PER-PLATFORM digest, queryable via `docker manifest inspect`.
package pins

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed builder_image_digests.json
var embeddedJSON []byte

// PinEntry is one row in the pin file. Ref is the canonical tag
// (e.g. `eclipse-temurin:8-jdk-jammy`); Platforms maps docker
// `--platform` strings to the per-arch image digest at that tag.
type PinEntry struct {
	Ref       string            `json:"ref"`
	Platforms map[string]string `json:"platforms"`
}

type pinFile struct {
	SchemaVersion string              `json:"schema_version"`
	Pins          map[string]PinEntry `json:"pins"`
}

var parsed = func() pinFile {
	var pf pinFile
	if err := json.Unmarshal(embeddedJSON, &pf); err != nil {
		// Build-time defect — the file is in our control, panicking
		// here just turns a corrupt JSON into a fast failure.
		panic("builder_image_digests.json is malformed: " + err.Error())
	}
	return pf
}()

// Resolve returns the canonical image reference (e.g.
// `eclipse-temurin:8-jdk-jammy@sha256:abc...`) for a given JDK
// version + docker platform. Returns (ref, digest, true) on hit;
// the digest is the per-arch image digest that docker actually
// stores locally — so `docker run image@digest` works without the
// "cannot overwrite digest" issue you'd hit with a manifest-list
// digest.
//
// If override is non-empty, it replaces the entire ref. The override
// path is documented in AGENTS.md as an escape hatch (FR-024) and
// participates in the cache key so pinned and overridden builds don't
// collide.
func Resolve(jdkVersion, platform, override string) (ref string, digest string, ok bool) {
	if override != "" {
		// Override is reported as the cache digest verbatim so any
		// bump in the override automatically changes the cache key.
		return override, override, true
	}
	entry, hit := parsed.Pins[jdkVersion]
	if !hit {
		return "", "", false
	}
	d, hit := entry.Platforms[platform]
	if !hit {
		return "", "", false
	}
	return fmt.Sprintf("%s@%s", entry.Ref, d), d, true
}

// Versions returns the list of JDK versions for which a pin exists,
// for diagnostic surfaces (preflight, error messages).
func Versions() []string {
	out := make([]string, 0, len(parsed.Pins))
	for v := range parsed.Pins {
		out = append(out, v)
	}
	return out
}

// Platforms returns the docker platform strings for which a pin
// exists under the given JDK version, used for diagnostic surfaces
// when Resolve misses on the platform side.
func Platforms(jdkVersion string) []string {
	entry, hit := parsed.Pins[jdkVersion]
	if !hit {
		return nil
	}
	out := make([]string, 0, len(entry.Platforms))
	for p := range entry.Platforms {
		out = append(out, p)
	}
	return out
}
