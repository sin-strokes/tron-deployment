package build

import (
	"strings"
	"testing"
)

// TestArchTripletForPlatform pins the platform → Debian multi-arch
// triplet mapping. The Dockerfile's LD_PRELOAD path embeds the
// triplet, so a typo here = broken tcmalloc at runtime = silent
// performance regression.
func TestArchTripletForPlatform(t *testing.T) {
	cases := []struct {
		platform string
		want     string
	}{
		{"linux/amd64", "x86_64-linux-gnu"},
		{"linux/arm64", "aarch64-linux-gnu"},
		{"", "x86_64-linux-gnu"},              // default
		{"linux/ppc64le", "x86_64-linux-gnu"}, // unsupported → safe fallback
	}
	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			if got := archTripletForPlatform(tc.platform); got != tc.want {
				t.Errorf("archTripletForPlatform(%q) = %q; want %q",
					tc.platform, got, tc.want)
			}
		})
	}
}

// TestJarWrapDockerfileTemplate_HasAllPlaceholders enforces that
// every placeholder image_wrap.go substitutes is actually present
// in the embedded template. If someone adds a new placeholder to
// the rendering code but forgets to use it in the Dockerfile (or
// vice versa — adds a {{X}} to the Dockerfile that nobody
// substitutes), trond's `docker build` would either fail or leak a
// literal `{{X}}` into the image.
func TestJarWrapDockerfileTemplate_HasAllPlaceholders(t *testing.T) {
	required := []string{
		"{{BASE_IMAGE}}",
		"{{JAR_NAME}}",
		"{{ARCH_TRIPLET}}",
		"{{SOURCE_REVISION}}",
		"{{CACHE_KEY}}",
		"{{BUILD_TIME}}",
	}
	for _, ph := range required {
		t.Run(ph, func(t *testing.T) {
			if !strings.Contains(jarWrapDockerfileTemplate, ph) {
				t.Errorf("template missing placeholder %q; image_wrap.go won't have anything to substitute", ph)
			}
		})
	}

	// Reverse direction: no leftover {{...}} should remain after a
	// stub substitution. Catches template typos (e.g. {{BASE-IMAGE}}
	// or {{JarName}}) that escape unit tests above.
	rendered := strings.NewReplacer(
		"{{BASE_IMAGE}}", "stub-image:latest@sha256:0",
		"{{JAR_NAME}}", "FullNode.jar",
		"{{ARCH_TRIPLET}}", "x86_64-linux-gnu",
		"{{SOURCE_REVISION}}", "0000000000000000000000000000000000000000",
		"{{CACHE_KEY}}", "stub-key",
		"{{BUILD_TIME}}", "2026-01-01T00:00:00Z",
	).Replace(jarWrapDockerfileTemplate)
	if strings.Contains(rendered, "{{") {
		// Find and report what's left.
		idx := strings.Index(rendered, "{{")
		end := strings.Index(rendered[idx:], "}}")
		var ctx string
		if end > 0 {
			ctx = rendered[idx : idx+end+2]
		}
		t.Errorf("Dockerfile template has unsubstituted placeholder: %s", ctx)
	}
}

// TestJarWrapDockerfile_HasTcmalloc is the Phase 5d.1 regression
// guard: the upstream tron-docker image relies on tcmalloc to keep
// java-tron's allocator pressure manageable under sustained RPS.
// If a future template refactor drops this, java-tron runs but
// perf regresses silently. This test pins it.
func TestJarWrapDockerfile_HasTcmalloc(t *testing.T) {
	checks := []string{
		"libtcmalloc-minimal4",     // apt-get install
		"libtcmalloc_minimal.so.4", // LD_PRELOAD path
		"LD_PRELOAD",
		"TCMALLOC_RELEASE_RATE",
	}
	for _, c := range checks {
		t.Run(c, func(t *testing.T) {
			if !strings.Contains(jarWrapDockerfileTemplate, c) {
				t.Errorf("template missing %q — tcmalloc setup broken", c)
			}
		})
	}
}

// TestJarWrapDockerfile_HasOCILabels enforces that the OCI label
// set tron-docker users expect (and downstream tooling like
// `docker inspect` / Grafana / etc. depends on) is present.
func TestJarWrapDockerfile_HasOCILabels(t *testing.T) {
	required := []string{
		"org.opencontainers.image.title",
		"org.opencontainers.image.source",
		"org.opencontainers.image.revision",
		"org.opencontainers.image.created",
		"trond.cache_key",
	}
	for _, label := range required {
		t.Run(label, func(t *testing.T) {
			if !strings.Contains(jarWrapDockerfileTemplate, label) {
				t.Errorf("Dockerfile missing OCI label %q", label)
			}
		})
	}
}
