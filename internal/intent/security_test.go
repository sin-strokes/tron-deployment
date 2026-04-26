package intent

import (
	"strings"
	"testing"
)

// security_test.go locks down the SECURITY-1 / SECURITY-2 fixes so the
// holes audited in 2026-04 stay fixed. Every test here demonstrates an
// attack vector that used to succeed before the safe_string validator
// + manual map/slice walk was added.

func TestSafeString_RejectsNewlineInExtraEnv(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    extra_env:
      FOO: "bar\nUser=root\nExecStartPre=/bin/sh -c id"
`)
	_, err := Parse(yaml)
	if err == nil {
		t.Fatal("expected newline-in-extra_env to be rejected")
	}
	if !strings.Contains(err.Error(), "newline") && !strings.Contains(err.Error(), "control char") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSafeString_RejectsNewlineInExtraArgs(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    extra_args: ["--debug\nUser=root"]
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected newline-in-extra_args to be rejected")
	}
}

func TestSafeString_RejectsNewlineInLabels(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    labels:
      role: "api\n    privileged: true"
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected newline-in-labels to be rejected")
	}
}

func TestSafeString_RejectsNewlineInNetworks(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    networks:
      - "mesh\n    privileged: true"
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected newline-in-networks to be rejected")
	}
}

func TestSafeString_RejectsNewlineInStorage(t *testing.T) {
	cases := []string{
		`storage: {data: "/var\n    privileged: true"}`,
		`storage: {logs: "/var\nx: y"}`,
		`storage: {path: "/opt/tron\nUser=root"}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			yaml := []byte("name: x\nnetwork: mainnet\ntarget: {type: local}\nnodes:\n  - type: fullnode\n    " + body + "\n")
			if _, err := Parse(yaml); err == nil {
				t.Fatalf("expected newline rejected for: %s", body)
			}
		})
	}
}

func TestSafeString_RejectsNewlineInInstallPathAndSystemUser(t *testing.T) {
	cases := []string{
		`install_path: "/opt/tron\nUser=root"`,
		`system_user: "tron\nUser=root"`,
		`image: "tronprotocol/java-tron\n    privileged: true"`,
		`version: "latest\n    privileged: true"`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			yaml := []byte("name: x\nnetwork: mainnet\ntarget: {type: local}\nnodes:\n  - type: fullnode\n    " + body + "\n")
			if _, err := Parse(yaml); err == nil {
				t.Fatalf("expected rejection for: %s", body)
			}
		})
	}
}

func TestSafeString_RejectsNewlineInJVMHeap(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    jvm:
      heap_max: "8g\nUser=root"
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected newline-in-jvm.heap_max to be rejected")
	}
}

func TestSafeString_RejectsNewlineInNetworkOverrides(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    network_overrides:
      seeds: ["1.2.3.4:18888\nblock.needSyncCheck = true"]
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected newline-in-seeds to be rejected")
	}
}

func TestSafeString_AllowsTabAndPrintable(t *testing.T) {
	// Tab is allowed (we treat it like printable). High-Unicode is fine.
	yaml := []byte(`
name: x
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
    labels:
      desc: "tabbed\tvalue 中文 ok"
`)
	if _, err := Parse(yaml); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- jar URL hardening ---

func TestJarURL_RejectsHTTP(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local, runtime: jar}
nodes:
  - type: fullnode
    jar:
      url: http://example.com/FullNode.jar
      sha256: 0000000000000000000000000000000000000000000000000000000000000000
`)
	_, err := Parse(yaml)
	if err == nil {
		t.Fatal("expected http:// jar URL to be rejected")
	}
}

func TestJarURL_RejectsFileScheme(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local, runtime: jar}
nodes:
  - type: fullnode
    jar:
      url: file:///etc/passwd
      sha256: 0000000000000000000000000000000000000000000000000000000000000000
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected file:// jar URL to be rejected")
	}
}

func TestJarURL_RequiresSHA256WhenURLSet(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local, runtime: jar}
nodes:
  - type: fullnode
    jar:
      url: https://example.com/FullNode.jar
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected jar.url without sha256 to be rejected")
	}
}

func TestJarURL_RejectsMalformedSHA256(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local, runtime: jar}
nodes:
  - type: fullnode
    jar:
      url: https://example.com/FullNode.jar
      sha256: notahex
`)
	if _, err := Parse(yaml); err == nil {
		t.Fatal("expected non-hex sha256 to be rejected")
	}
}

func TestJarURL_HappyPath(t *testing.T) {
	yaml := []byte(`
name: x
network: mainnet
target: {type: local, runtime: jar}
nodes:
  - type: fullnode
    jar:
      url: https://example.com/FullNode.jar
      sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)
	if _, err := Parse(yaml); err != nil {
		t.Errorf("expected valid jar block to parse: %v", err)
	}
}
