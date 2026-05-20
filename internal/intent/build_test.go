package intent

import (
	"strings"
	"testing"
)

// TestParse_BuildAndImageMutuallyExclusive is the FR-005 regression
// guard at intent-load time: a node may declare exactly one artifact
// source — `build:` OR `image:` OR `jar:`. Two simultaneously must
// be rejected with a clear message.
func TestParse_BuildAndImageMutuallyExclusive(t *testing.T) {
	data := []byte(`
name: bad-mix
network: nile
target:
  type: local
nodes:
  - type: fullnode
    image: tronprotocol/java-tron:latest
    build:
      source: /tmp/java-tron
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected validation error for build + image combination")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should mention mutual exclusion", err)
	}
}

func TestParse_BuildAndJarMutuallyExclusive(t *testing.T) {
	data := []byte(`
name: bad-mix
network: nile
target:
  type: local
nodes:
  - type: fullnode
    jar:
      url: https://example.com/x.jar
      sha256: 0000000000000000000000000000000000000000000000000000000000000000
    build:
      source: /tmp/java-tron
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected validation error for build + jar combination")
	}
}

// TestParse_BuildImageArtifactRequiresTag asserts the convenience
// check we emit at intent-load time so users get an obvious error
// before the apply pipeline even tries to resolve the build.
func TestParse_BuildImageArtifactRequiresTag(t *testing.T) {
	data := []byte(`
name: missing-tag
network: nile
target:
  type: local
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected validation error for artifact=image without image_tag")
	}
	if !strings.Contains(err.Error(), "image_tag") {
		t.Errorf("error %q should mention image_tag", err)
	}
}

// TestParse_BuildOnly is the happy path: a node with just `build:`
// (no image / no jar) parses cleanly.
func TestParse_BuildOnly(t *testing.T) {
	data := []byte(`
name: dev-fullnode
network: nile
target:
  type: local
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      revision: HEAD
      jdk: "8"
      artifact: jar
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if i.Nodes[0].Build == nil {
		t.Fatal("Build field not set after parse")
	}
	if i.Nodes[0].Build.Source != "/tmp/java-tron" {
		t.Errorf("source = %q; want /tmp/java-tron", i.Nodes[0].Build.Source)
	}
	if i.Nodes[0].Build.JDK != "8" {
		t.Errorf("jdk = %q; want 8", i.Nodes[0].Build.JDK)
	}
}

// TestParse_BuildAppliesDefaults is the regression guard for the
// review-pass-2 fix that taught ApplyDefaults to fill BuildSpec
// fields at intent-load time so `config validate --explain` and
// downstream consumers see canonical values. Build's own
// withDefaults() still owns the source of truth; the two stay in
// lockstep via this test.
//
// Platform is set explicitly so the JDK default is deterministic
// across host architectures (arch-aware defaults are covered in
// TestParse_BuildJDKDefaultsByPlatform).
func TestParse_BuildAppliesDefaults(t *testing.T) {
	data := []byte(`
name: dev-fullnode
network: nile
target:
  type: local
  runtime: jar
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      platform: linux/amd64
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if i.Nodes[0].Build == nil {
		t.Fatal("Build block missing post-parse")
	}
	b := i.Nodes[0].Build
	if b.Revision != "HEAD" {
		t.Errorf("Revision default = %q; want HEAD", b.Revision)
	}
	if b.JDK != "8" {
		t.Errorf("JDK default = %q; want 8 (platform=linux/amd64)", b.JDK)
	}
	if b.Artifact != "jar" {
		t.Errorf("Artifact default = %q; want jar", b.Artifact)
	}
	if b.Builder != "docker" {
		t.Errorf("Builder default = %q; want docker", b.Builder)
	}
	if b.GradleTask != "shadowJar" {
		t.Errorf("GradleTask default = %q; want shadowJar (artifact=jar)", b.GradleTask)
	}
	if b.Platform != "linux/amd64" {
		t.Errorf("Platform = %q; want linux/amd64 (preserved as written)", b.Platform)
	}
}

// TestParse_BuildJDKDefaultsByPlatform pins the java-tron compat
// matrix: amd64 → JDK 8, arm64 → JDK 17. The platform field drives
// the JDK default; users get the supported combo without writing
// either field explicitly (when host arch happens to match the
// target arch).
func TestParse_BuildJDKDefaultsByPlatform(t *testing.T) {
	cases := []struct {
		platform string
		wantJDK  string
	}{
		{"linux/amd64", "8"},
		{"linux/arm64", "17"},
	}
	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			data := []byte(`
name: x
network: nile
target:
  type: local
  runtime: jar
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      platform: ` + tc.platform + `
`)
			i, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := i.Nodes[0].Build.JDK; got != tc.wantJDK {
				t.Errorf("platform=%s → JDK default = %q; want %q",
					tc.platform, got, tc.wantJDK)
			}
		})
	}
}

// TestParse_BuildPlatformDefaultsToHostArch asserts the platform
// itself defaults when omitted — to the host arch's docker triple.
// Test is host-arch-aware so it works on both amd64 and arm64 CI.
func TestParse_BuildPlatformDefaultsToHostArch(t *testing.T) {
	data := []byte(`
name: x
network: nile
target:
  type: local
  runtime: jar
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := i.Nodes[0].Build.Platform
	expected := DefaultPlatform()
	if got != expected {
		t.Errorf("Platform default = %q; want %q (host arch)", got, expected)
	}
}

// TestParse_BuildSuppressesImageDefault asserts the
// applyNodeDefaults change: when Build is present, the legacy
// Image default ("tronprotocol/java-tron") MUST be suppressed.
// Otherwise the intent ends up with both Build AND Image set
// post-defaults — violating the mutex and risking a docker compose
// rendering an unintended image: field.
func TestParse_BuildSuppressesImageDefault(t *testing.T) {
	data := []byte(`
name: dev-fullnode
network: nile
target:
  type: local
  runtime: jar
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if i.Nodes[0].Image != "" {
		t.Errorf("Image should remain empty when Build is set; got %q", i.Nodes[0].Image)
	}
}

// TestParse_BuildDefaultsRuntimeToJar is the Phase 2 review-pass-3
// regression guard. An intent with `build:` and no explicit
// `target.runtime` MUST default to jar (the only Phase 2 wired
// path) — defaulting to docker would silently put the intent into
// a configuration that validateOptions rejects, making the most
// natural dev intent unwritable.
func TestParse_BuildDefaultsRuntimeToJar(t *testing.T) {
	data := []byte(`
name: dev-fullnode
network: nile
target:
  type: local
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if i.Target.Runtime != "jar" {
		t.Errorf("Target.Runtime default = %q; want jar (intent has build)", i.Target.Runtime)
	}
}

// TestParse_NoBuildKeepsDockerDefault: when no node has `build:`,
// the legacy docker default still applies.
func TestParse_NoBuildKeepsDockerDefault(t *testing.T) {
	data := []byte(`
name: prod-fullnode
network: nile
target:
  type: local
nodes:
  - type: fullnode
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if i.Target.Runtime != "docker" {
		t.Errorf("Target.Runtime default = %q; want docker", i.Target.Runtime)
	}
}

// TestParse_BuildRuntimeArtifactMismatch is the Validate-time check
// (was apply-time only before review-pass-3). Catching it at
// `trond config validate` saves a deploy attempt.
func TestParse_BuildRuntimeArtifactMismatch(t *testing.T) {
	cases := []struct {
		name     string
		runtime  string
		artifact string
	}{
		{"docker+jar", "docker", "jar"},
		{"jar+image", "jar", "image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`
name: x
network: nile
target:
  type: local
  runtime: ` + tc.runtime + `
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: ` + tc.artifact + `
      image_tag: trond-test:dev
`)
			_, err := Parse(data)
			if err == nil {
				t.Fatalf("expected validation error for runtime=%s + artifact=%s",
					tc.runtime, tc.artifact)
			}
		})
	}
}

// TestParse_BuildDockerImageNowAccepted is the Phase 3 unlock
// regression guard. Phase 2 rejected `runtime: docker +
// artifact: image` because the compose render path didn't know
// about locally-built images. Phase 3 wires it in: the same intent
// must now parse + validate cleanly.
func TestParse_BuildDockerImageNowAccepted(t *testing.T) {
	data := []byte(`
name: dev-fullnode
network: nile
target:
  type: local
  runtime: docker
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build:dev
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse should now accept docker+image: %v", err)
	}
	if i.Target.Runtime != "docker" {
		t.Errorf("Target.Runtime = %q; want docker (explicit)", i.Target.Runtime)
	}
	if i.Nodes[0].Build.Artifact != "image" {
		t.Errorf("Build.Artifact = %q; want image", i.Nodes[0].Build.Artifact)
	}
}

// TestParse_BuildImageArtifactDefaultsRuntimeToDocker is the
// inverse of TestParse_BuildDefaultsRuntimeToJar: when the user
// declares artifact=image without an explicit target.runtime, the
// runtime MUST default to docker (the artifact's only consumer).
func TestParse_BuildImageArtifactDefaultsRuntimeToDocker(t *testing.T) {
	data := []byte(`
name: dev-fullnode
network: nile
target:
  type: local
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build:dev
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if i.Target.Runtime != "docker" {
		t.Errorf("Target.Runtime default = %q; want docker (artifact=image)",
			i.Target.Runtime)
	}
}

// TestParse_BuildImageRejectsCrossArchPlatform is the Phase 3
// review pass 1 regression guard for the most-dangerous Phase 3
// combo: artifact=image with a platform that differs from the host's
// arch. The docker.sock-mounted builder can't actually produce a
// cross-arch image (host daemon wins); silently accepting it would
// cache an amd64 image under an arm64 cache key (or vice versa)
// and deploy the wrong arch to the target server.
func TestParse_BuildImageRejectsCrossArchPlatform(t *testing.T) {
	host := DefaultPlatform()
	var crossArch string
	if host == "linux/arm64" {
		crossArch = "linux/amd64"
	} else {
		crossArch = "linux/arm64"
	}
	data := []byte(`
name: bad-cross
network: nile
target:
  type: local
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build:dev
      platform: ` + crossArch + `
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatalf("expected rejection of artifact=image + platform=%q on host=%q",
			crossArch, host)
	}
	if !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("error %q should explain the cross-arch hazard", err)
	}
}

// TestParse_BuildImageAcceptsHostArchPlatform: same combo but
// platform equals the host arch — the safe case — must still parse.
func TestParse_BuildImageAcceptsHostArchPlatform(t *testing.T) {
	host := DefaultPlatform()
	data := []byte(`
name: ok-same-arch
network: nile
target:
  type: local
  runtime: docker
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build:dev
      platform: ` + host + `
`)
	if _, err := Parse(data); err != nil {
		t.Fatalf("host-arch image build should parse: %v", err)
	}
}

// TestParse_BuildImageStrategyJarWrap_AllowsCrossArch is the Phase
// 5d unlock guard. When users opt into `image_strategy: jar-wrap`,
// the runtime/artifact validator MUST NOT reject cross-arch builds
// (the docker.sock-mounted gradle path is the unsafe one; jar-wrap
// runs docker build directly from host where --platform works).
func TestParse_BuildImageStrategyJarWrap_AllowsCrossArch(t *testing.T) {
	host := DefaultPlatform()
	var crossArch string
	if host == "linux/arm64" {
		crossArch = "linux/amd64"
	} else {
		crossArch = "linux/arm64"
	}
	data := []byte(`
name: ok-cross-arch
network: nile
target:
  type: local
  runtime: docker
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build/dev:foo
      image_strategy: jar-wrap
      platform: ` + crossArch + `
`)
	if _, err := Parse(data); err != nil {
		t.Fatalf("jar-wrap should permit cross-arch (unlike gradle strategy): %v", err)
	}
}

// TestParse_BuildImageStrategyGradle_RejectsCrossArch is the
// regression guard for the OTHER half — gradle strategy must keep
// rejecting cross-arch (the docker.sock hazard is real for that
// path).
func TestParse_BuildImageStrategyGradle_RejectsCrossArch(t *testing.T) {
	host := DefaultPlatform()
	var crossArch string
	if host == "linux/arm64" {
		crossArch = "linux/amd64"
	} else {
		crossArch = "linux/arm64"
	}
	data := []byte(`
name: bad-cross-arch
network: nile
target:
  type: local
  runtime: docker
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build/dev:foo
      image_strategy: gradle
      platform: ` + crossArch + `
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("gradle strategy must still reject cross-arch")
	}
}

// TestParse_BuildImageStrategyDefaultsToGradle: omitting the field
// must keep the Phase 3 default (gradle), so existing tron-docker-
// shaped intents continue working unchanged.
func TestParse_BuildImageStrategyDefaultsToGradle(t *testing.T) {
	data := []byte(`
name: default-strategy
network: nile
target:
  type: local
  runtime: docker
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      artifact: image
      image_tag: trond-build/dev:foo
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := i.Nodes[0].Build.ImageStrategy; got != "gradle" {
		t.Errorf("image_strategy default = %q; want gradle", got)
	}
}

// TestParse_BuildInvalidJDK pins the validator-tag enum.
func TestParse_BuildInvalidJDK(t *testing.T) {
	data := []byte(`
name: bad-jdk
network: nile
target:
  type: local
nodes:
  - type: fullnode
    build:
      source: /tmp/java-tron
      jdk: "7"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected validation error for jdk: 7")
	}
}
