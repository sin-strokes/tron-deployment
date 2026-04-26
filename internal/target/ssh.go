package target

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/tronprotocol/tron-deployment/internal/security"
)

// SSHTarget executes commands and file operations on a remote machine via SSH.
//
// Security hardening:
//   - Commands are validated against an allowlist before execution (see security.ValidateCommand)
//   - Host keys are verified against known_hosts files
//   - Paths passed to shell commands are single-quoted to neutralize metacharacters
//   - Long-running commands respect context cancellation
type SSHTarget struct {
	host           string
	port           int
	user           string
	identityFile   string
	knownHostsFile string // Path to known_hosts; empty uses ~/.ssh/known_hosts
	client         *ssh.Client
}

// NewSSHTarget creates a new SSHTarget. Call Connect() before use.
func NewSSHTarget(host string, port int, user, identityFile string) *SSHTarget {
	if port == 0 {
		port = 22
	}
	return &SSHTarget{
		host:         host,
		port:         port,
		user:         user,
		identityFile: identityFile,
	}
}

// WithKnownHosts overrides the known_hosts file path used for host key verification.
func (t *SSHTarget) WithKnownHosts(path string) *SSHTarget {
	t.knownHostsFile = path
	return t
}

// Connect establishes the SSH connection, verifying the server's host key.
func (t *SSHTarget) Connect() error {
	identityPath, err := expandHome(t.identityFile)
	if err != nil {
		return fmt.Errorf("expand identity path: %w", err)
	}
	keyData, err := os.ReadFile(identityPath)
	if err != nil {
		return fmt.Errorf("read identity file %s: %w", identityPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parse identity file: %w", err)
	}

	hostKeyCallback, err := t.hostKeyCallback()
	if err != nil {
		return fmt.Errorf("load known_hosts: %w", err)
	}

	config := &ssh.ClientConfig{
		User: t.user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
	}

	addr := net.JoinHostPort(t.host, strconv.Itoa(t.port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("ssh connect to %s: %w", addr, err)
	}

	t.client = client
	return nil
}

// hostKeyCallback returns a verifier backed by known_hosts. Falls back to
// ~/.ssh/known_hosts when no explicit file is configured.
//
// If TROND_SSH_ACCEPT_NEW_HOSTS=1 is set, an unknown host is accepted and
// appended to known_hosts. This is opt-in — by default an unknown host is
// rejected to prevent MITM.
func (t *SSHTarget) hostKeyCallback() (ssh.HostKeyCallback, error) {
	path := t.knownHostsFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find home dir: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create empty known_hosts so knownhosts.New doesn't fail
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		f.Close()
	}

	base, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}

	acceptNew := os.Getenv("TROND_SSH_ACCEPT_NEW_HOSTS") == "1"

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		cbErr := base(hostname, remote, key)
		if cbErr == nil {
			return nil
		}

		// Distinguish three cases the knownhosts package can surface:
		//   1) *KeyError with len(Want) > 0 — the offered key MISMATCHES a
		//      pinned key. This is a likely MITM and must NEVER be auto-trusted,
		//      regardless of TROND_SSH_ACCEPT_NEW_HOSTS.
		//   2) *KeyError with len(Want) == 0 — host has not been seen before.
		//      Eligible for opt-in TOFU.
		//   3) Any other error (parse error, I/O failure on known_hosts, …) —
		//      reject. We will not auto-trust through an opaque failure.
		var keyErr *knownhosts.KeyError
		if !errors.As(cbErr, &keyErr) {
			return fmt.Errorf("host key check for %s: %w", hostname, cbErr)
		}
		if len(keyErr.Want) > 0 {
			return fmt.Errorf("host key MISMATCH for %s — possible MITM, refusing to trust: %w", hostname, cbErr)
		}
		if !acceptNew {
			return fmt.Errorf("host key verification failed for %s: %w (set TROND_SSH_ACCEPT_NEW_HOSTS=1 to trust new hosts)", hostname, cbErr)
		}

		// Opt-in TOFU: append the new host's key to known_hosts.
		f, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if openErr != nil {
			return fmt.Errorf("append known_hosts: %w", openErr)
		}
		defer f.Close()
		line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
		if _, err := fmt.Fprintln(f, line); err != nil {
			return fmt.Errorf("write known_hosts: %w", err)
		}
		return nil
	}, nil
}

// Close closes the SSH connection.
func (t *SSHTarget) Close() error {
	if t.client != nil {
		return t.client.Close()
	}
	return nil
}

// Exec runs a command on the remote host. The command name is validated
// against the SSH allowlist (see internal/security). Context cancellation
// is honored by sending a signal and closing the session.
func (t *SSHTarget) Exec(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	if t.client == nil {
		return nil, fmt.Errorf("ssh not connected")
	}

	if err := security.ValidateCommand(cmd); err != nil {
		return nil, err
	}

	session, err := t.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	defer session.Close()

	fullCmd := quoteArgs(cmd, args)

	var combined bytes.Buffer
	session.Stdout = &combined
	session.Stderr = &combined

	done := make(chan error, 1)
	go func() { done <- session.Run(fullCmd) }()

	select {
	case runErr := <-done:
		if runErr != nil {
			return combined.Bytes(), fmt.Errorf("ssh exec %q: %w: %s", fullCmd, runErr, combined.String())
		}
		return combined.Bytes(), nil
	case <-ctx.Done():
		// Best-effort signal; some sshd configs ignore SIGTERM over the protocol.
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		return combined.Bytes(), ctx.Err()
	}
}

func (t *SSHTarget) Upload(ctx context.Context, localPath, remotePath string) error {
	if t.client == nil {
		return fmt.Errorf("ssh not connected")
	}

	localData, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	return t.writeRemoteFile(ctx, remotePath, localData, 0644)
}

func (t *SSHTarget) Download(ctx context.Context, remotePath, localPath string) error {
	if t.client == nil {
		return fmt.Errorf("ssh not connected")
	}

	data, err := t.readRemoteFile(ctx, remotePath)
	if err != nil {
		return err
	}

	return os.WriteFile(localPath, data, 0644)
}

func (t *SSHTarget) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return t.readRemoteFile(ctx, path)
}

func (t *SSHTarget) WriteFile(ctx context.Context, path string, data []byte, perm os.FileMode) error {
	return t.writeRemoteFile(ctx, path, data, perm)
}

func (t *SSHTarget) DiskFree(ctx context.Context, path string) (uint64, error) {
	out, err := t.Exec(ctx, "df", "--output=avail", "-k", path)
	if err != nil {
		return 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output")
	}

	kb, err := strconv.ParseUint(strings.TrimSpace(lines[len(lines)-1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse disk free: %w", err)
	}

	return kb * 1024, nil
}

func (t *SSHTarget) MemTotal(ctx context.Context) (uint64, error) {
	out, err := t.Exec(ctx, "grep", "MemTotal", "/proc/meminfo")
	if err != nil {
		return 0, err
	}

	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected meminfo output")
	}

	kb, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse memtotal: %w", err)
	}

	return kb * 1024, nil
}

func (t *SSHTarget) String() string {
	return fmt.Sprintf("ssh://%s@%s:%d", t.user, t.host, t.port)
}

// readRemoteFile reads a file from the remote host using cat over SSH.
// The path is single-quoted to prevent shell interpretation.
func (t *SSHTarget) readRemoteFile(ctx context.Context, path string) ([]byte, error) {
	out, err := t.Exec(ctx, "cat", path)
	if err != nil {
		return nil, fmt.Errorf("read remote file %s: %w", path, err)
	}
	return out, nil
}

// writeRemoteFile writes data to a remote file. The path is single-quoted to
// neutralize shell metacharacters; the data is streamed via stdin so it never
// touches the command line.
func (t *SSHTarget) writeRemoteFile(ctx context.Context, path string, data []byte, perm os.FileMode) error {
	if err := security.ValidateCommand("tee"); err != nil {
		return err
	}

	session, err := t.client.NewSession()
	if err != nil {
		return fmt.Errorf("new ssh session: %w", err)
	}
	defer session.Close()

	session.Stdin = bytes.NewReader(data)

	quotedPath := shellQuote(path)
	quotedDir := shellQuote(filepath.Dir(path))
	cmd := fmt.Sprintf("mkdir -p %s && tee %s > /dev/null && chmod %o %s",
		quotedDir, quotedPath, perm, quotedPath)

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()

	select {
	case runErr := <-done:
		if runErr != nil {
			return fmt.Errorf("write remote file %s: %w", path, runErr)
		}
		return nil
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		return ctx.Err()
	}
}

// quoteArgs joins cmd and args with EVERY token single-quoted so shell
// metacharacters anywhere in the call cannot break out. We quote `cmd`
// too — even though every internal call site today passes a single
// whitelist-validated word, the contract should hold defensively against
// future callers passing `cmd = "docker --tlscert /tmp/x"` or similar.
func quoteArgs(cmd string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(cmd))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// expandHome resolves a leading "~" or "~/" to the user's home directory.
// SSH identity_file is commonly given as "~/.ssh/id_rsa" in intent files —
// os.ReadFile does not interpret ~ so we expand it ourselves.
func expandHome(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	// "~user/foo" form is not supported — refuse rather than guess.
	return "", fmt.Errorf("unsupported home expansion: %s", path)
}

// shellQuote returns s wrapped in single quotes with any embedded single
// quotes escaped. Output is safe to interpolate into a POSIX shell command.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Replace embedded ' with '\''
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Ensure SSHTarget implements Target.
var _ Target = (*SSHTarget)(nil)

// Ensure io.Closer is implemented.
var _ io.Closer = (*SSHTarget)(nil)
