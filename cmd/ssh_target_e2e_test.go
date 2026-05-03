//go:build e2e

package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pickFreePort asks the OS for a free TCP port by binding to :0 and
// reading back the address; closes immediately so docker can rebind.
// There's a small TOCTOU race — acceptable for tests.
func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func probeTCPSSH(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// TestE2E_SSHTarget_BasicLifecycle exercises the SSH target code
// path against a containerised SSH server. Without this test, the
// SSH path is only validated by unit tests with a mocked transport,
// which can't catch real-world wire-protocol issues, host-key
// verification, or PATH/exec gotchas.
//
// The fixture spins up linuxserver/openssh-server (alpine-based,
// ~30 MB pull) on a random host port, generates an ed25519 keypair,
// and feeds the public key to the container. The trond binary then
// connects via the configured SSH target and runs a few read-side
// operations.
//
// We can't fully drive an `apply` over SSH because the SSH container
// doesn't have docker installed (and starting docker-in-docker is a
// fragile rabbit hole). Instead we test the SSH transport itself —
// the deploy path is exercised by the local Docker e2e tests.
//
// Skipped when Docker isn't available.
func TestE2E_SSHTarget_BasicLifecycle(t *testing.T) {
	skipUnlessDocker(t)

	fixture := startSSHFixture(t)
	defer fixture.cleanup()

	stateDir, env := e2eEnv(t)
	env = append(env,
		"TROND_SSH_ACCEPT_NEW_HOSTS=1", // first connect: trust + record host key
	)

	// Write an intent that points at the SSH fixture. We use the
	// jar runtime (not docker) so apply doesn't try to call docker
	// inside the SSH container, which doesn't have it. apply will
	// fail later because there's no JDK there either, but we get
	// to test the SSH transport for everything before that point
	// (target resolution, preflight's ssh_reachable check, etc.).
	intentPath := filepath.Join(stateDir, "ssh-intent.yaml")
	intent := fmt.Sprintf(`name: ssh-target-test
target:
  type: ssh
  host: 127.0.0.1
  port: %d
  user: trond
  identity_file: %s
  runtime: jar
network: nile
nodes:
  - type: fullnode
    version: latest
    install_path: /tmp/trond-ssh
    resources:
      memory: 4GB
    ports:
      http: 8090
      grpc: 50051
      p2p: 18888
`, fixture.port, fixture.privKeyPath)
	if err := os.WriteFile(intentPath, []byte(intent), 0o600); err != nil {
		t.Fatalf("write intent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. config validate — pure local op, sanity check.
	out := runTrondCtx(ctx, t, env, "config", "validate", intentPath, "--output", "json")
	if !strings.Contains(string(out), `"valid": true`) {
		t.Fatalf("validate failed:\n%s", out)
	}

	// 2. preflight — exercises the SSH connect + host-key trust +
	//    Exec("java -version") path. Java isn't installed in the
	//    fixture so the JDK check returns "fail", but the exec
	//    plumbing went through SSH. That's the real coverage.
	out, err := runTrondAllowFail(ctx, t, env,
		"preflight", "--intent", intentPath, "--output", "json")
	t.Logf("preflight output:\n%s", out)
	// Preflight returns exit 4 (PREFLIGHT_FAILURE) when any check
	// fails. We tolerate that — the test asserts that the SSH
	// transport got far enough to RUN the checks (vs. failing at
	// connect time).
	body := string(out)
	if !strings.Contains(body, `"target": "ssh"`) {
		t.Errorf("preflight didn't recognise SSH target:\n%s", out)
	}
	if !strings.Contains(body, "checks") {
		t.Errorf("preflight didn't reach the check loop (SSH transport broken?):\n%s", out)
	}
	_ = err
}

// sshFixture is the test container + key pair we tear down after.
type sshFixture struct {
	containerID string
	port        int
	privKeyPath string
	cleanup     func()
}

// startSSHFixture launches linuxserver/openssh-server on a random
// port, waits for it to accept connections, and returns the address.
// linuxserver's image accepts a PUBLIC_KEY env var on first boot to
// preinstall an authorized key — that's how we get key-based access
// without an interactive password prompt.
func startSSHFixture(t *testing.T) *sshFixture {
	t.Helper()

	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "id_ed25519")
	pubKey := privKey + ".pub"

	// 1. Generate an ed25519 keypair via ssh-keygen (universally
	//    available; no Go-side keygen dependency).
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", privKey, "-q")
	if err := cmd.Run(); err != nil {
		t.Fatalf("ssh-keygen: %v", err)
	}
	pubKeyData, err := os.ReadFile(pubKey)
	if err != nil {
		t.Fatalf("read pub: %v", err)
	}

	// 2. Pick a random ephemeral port — easier than parsing docker's
	//    "0:32678" output. Bind to 0 then close so OS marks it free
	//    before docker tries to bind it (small race; if it bites in
	//    CI we can switch to docker's auto-assign + inspect).
	port := pickFreePort(t)

	// 3. Start the container in detached mode.
	out, err := exec.Command("docker", "run", "-d",
		"--rm",
		"-p", fmt.Sprintf("%d:2222", port),
		"-e", "PUID=1000",
		"-e", "PGID=1000",
		"-e", "USER_NAME=trond",
		"-e", "PUBLIC_KEY="+strings.TrimSpace(string(pubKeyData)),
		"linuxserver/openssh-server",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run failed: %v\noutput: %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
	}
	t.Cleanup(cleanup)

	// 4. Wait for SSH to accept connections. linuxserver's image
	//    prints the auto-generated host keys to its log; once we see
	//    "Server listening" or can TCP-connect, we proceed. We poll
	//    every 250ms for up to 30s.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if probeTCPSSH(port) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !probeTCPSSH(port) {
		t.Fatal("ssh fixture did not start listening within 30s")
	}

	return &sshFixture{
		containerID: containerID,
		port:        port,
		privKeyPath: privKey,
		cleanup:     cleanup,
	}
}
