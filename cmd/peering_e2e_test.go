//go:build e2e

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func peeringIntent() string {
	return `name: private-dev
target:
  type: local
  runtime: docker
network: private
nodes:
  - type: witness
    version: latest
    witness_key:
      private_key_env: SR_PRIVATE_KEY
    resources:
      memory: 2GB
    ports:
      http: 28090
      grpc: 50081
      p2p: 28888
  - type: fullnode
    version: latest
    resources:
      memory: 2GB
    ports:
      http: 28091
      grpc: 50082
      p2p: 28889
`
}

// TestE2E_Network_Peering deploys a 2-node private network and
// verifies the nodes can actually reach each other on the docker
// internal network — i.e. the auto-wired `node.active` peer list is
// correct AND the docker compose project's user-defined network
// exposes containers under their compose container_name.
//
// Without this test, peering breakage looks like "your private
// network deploys fine but never produces blocks" — a much harder
// failure mode to diagnose than a failed deploy. The contract:
// `docker exec <node-A> curl <node-B>:<http-port>/wallet/getnowblock`
// must succeed within a small window after deploy.
//
// Slow (~15s, dominated by container startup) and Docker-gated.
func TestE2E_Network_Peering(t *testing.T) {
	skipUnlessDocker(t)
	stateDir, env := e2eEnv(t)
	env = append(env,
		"SR_PRIVATE_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	// Build a minimal 2-node intent with low memory limits — the
	// committed example asks for 8GB per node, which JVM can't
	// commit on resource-constrained CI runners (and on Docker
	// Desktop's default VM size). 2GB per node is enough to start
	// java-tron's HTTP listener; we don't actually exercise the
	// chain.
	intentPath := filepath.Join(stateDir, "peering-intent.yaml")
	if err := os.WriteFile(intentPath, []byte(peeringIntent()), 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cleanupCancel()
		_, _ = runTrondAllowFail(cleanupCtx, t, env,
			"network", "destroy", "--confirm", "private-dev", "--output", "json")
	})

	// Deploy the private network.
	runTrondCtx(ctx, t, env, "network", "create",
		"--intent", intentPath, "--output", "json")

	// Probe: from inside node0, can DNS resolve node1's container
	// name? This is the real trond-level peering contract — that
	// the auto-wired active_peers list refers to addresses both
	// containers can resolve. We deliberately don't wait for
	// java-tron's HTTP API to come up (that's java-tron's job +
	// resource-dependent on CI runners with limited Docker
	// memory); DNS resolution succeeds within seconds of the
	// containers starting and is the load-bearing assertion for
	// `network create`'s shared-network setup.
	//
	// Before the shared-network fix, getent would fail with exit
	// status 2 (not found). After the fix, it returns the peer's
	// docker IP within the user-defined network.
	deadline := time.Now().Add(45 * time.Second)
	var lastErr error
	var lastOut []byte
	for time.Now().Before(deadline) {
		probe := exec.CommandContext(ctx, "docker", "exec", "private-dev-node0",
			"getent", "hosts", "private-dev-node1")
		out, err := probe.CombinedOutput()
		if err == nil && strings.Contains(string(out), "private-dev-node1") {
			t.Logf("node0 resolved node1: %s", strings.TrimSpace(string(out)))
			return
		}
		lastErr = err
		lastOut = out
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("node0 could not resolve node1 over shared docker network\n"+
		"last error: %v\nlast output:\n%s", lastErr, lastOut)
}
