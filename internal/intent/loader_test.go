package intent

import (
	"strings"
	"testing"
)

func TestParse_Minimal(t *testing.T) {
	data := []byte(`
name: test-node
network: mainnet
target:
  type: local
nodes:
  - type: fullnode
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("parse minimal: %v", err)
	}
	if i.Name != "test-node" {
		t.Errorf("name = %q, want test-node", i.Name)
	}
	if len(i.Nodes) != 1 || i.Nodes[0].Type != "fullnode" {
		t.Errorf("nodes mismatch: %+v", i.Nodes)
	}
}

func TestParse_InvalidName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"uppercase", "Test-Node", "invalid name"},
		{"leading hyphen", "-bad", "invalid name"},
		{"underscore", "bad_name", "invalid name"},
		{"too long", strings.Repeat("a", 64), "invalid name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := []byte("name: " + tc.input + "\nnetwork: mainnet\ntarget: {type: local}\nnodes: [{type: fullnode}]\n")
			_, err := Parse(yaml)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateWitnessKeyEnv(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid env name", "SR_PRIVATE_KEY", false},
		{"valid with digits", "KEY_1", false},
		{"hex key rejected", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
		{"0x prefix rejected", "0xabcd1234", true},
		{"lowercase 0x rejected", "0xdeadbeef", true},
		{"invalid char", "FOO-BAR", true},
		{"starts with digit", "1KEY", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWitnessKeyEnv(tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tc.value, err)
			}
		})
	}
}

func TestParse_WitnessRequiresKeyEnv(t *testing.T) {
	data := []byte(`
name: witness-test
network: mainnet
target: {type: local}
nodes:
  - type: witness
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for witness without witness_key_env")
	}
	if !strings.Contains(err.Error(), "witness_key_env") {
		t.Errorf("err = %v, want mentioning witness_key_env", err)
	}
}

func TestParse_InvalidNetwork(t *testing.T) {
	data := []byte(`
name: bad-net
network: bogus
target: {type: local}
nodes: [{type: fullnode}]
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for invalid network")
	}
}

func TestParse_SSHTargetRequiresHostAndUser(t *testing.T) {
	data := []byte(`
name: ssh-test
network: mainnet
target: {type: ssh}
nodes: [{type: fullnode}]
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for ssh target missing host/user")
	}
}

func TestApplyDefaults(t *testing.T) {
	data := []byte(`
name: default-test
network: mainnet
target: {type: local}
nodes:
  - type: fullnode
`)
	i, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if i.Target.Runtime == "" {
		t.Error("Target.Runtime default not applied")
	}
	if i.Nodes[0].Ports.HTTP == 0 {
		t.Error("Ports.HTTP default not applied")
	}
}
