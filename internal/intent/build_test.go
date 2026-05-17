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
func TestParse_BuildAppliesDefaults(t *testing.T) {
	// Minimal build block — only source provided.
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
	if i.Nodes[0].Build == nil {
		t.Fatal("Build block missing post-parse")
	}
	b := i.Nodes[0].Build
	if b.Revision != "HEAD" {
		t.Errorf("Revision default = %q; want HEAD", b.Revision)
	}
	if b.JDK != "8" {
		t.Errorf("JDK default = %q; want 8", b.JDK)
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
