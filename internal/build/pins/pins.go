// Package pins holds the trond release's pinned set of builder image
// digests, embedded into the binary so a deployed trond is the source
// of truth (no out-of-band JSON file the user could de-sync).
//
// Bump policy: a Makefile target `refresh-builder-pins` re-resolves
// Eclipse Temurin tags to current digests and rewrites the JSON. The
// regeneration happens at trond release-prep time, not at runtime.
package pins

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed builder_image_digests.json
var embeddedJSON []byte

// PinEntry is one row in the pin file — the ref name plus the
// content-addressed digest. The cache key (FR-002) consumes Digest.
type PinEntry struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
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
// version string ("8", "11", "17", "21"). Returns
// (ref, digest, true) on hit. Caller threads the digest into the
// cache key.
//
// If override is non-empty, it replaces the entire ref. The override
// path is documented in AGENTS.md as an escape hatch (FR-024) and
// participates in the cache key so pinned and overridden builds don't
// collide.
func Resolve(jdkVersion string, override string) (ref string, digest string, ok bool) {
	if override != "" {
		// Override must already include the digest portion. Caller is
		// responsible for that — pins.go just lets it through. The
		// "digest" reported back to the cache key is the override
		// itself, so any bump in the override automatically changes
		// the cache key.
		return override, override, true
	}
	entry, hit := parsed.Pins[jdkVersion]
	if !hit {
		return "", "", false
	}
	return fmt.Sprintf("%s@%s", entry.Ref, entry.Digest), entry.Digest, true
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
