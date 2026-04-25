package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tronprotocol/tron-deployment/internal/target"
)

// JarRuntime manages nodes via direct jar execution + systemd.
type JarRuntime struct {
	target target.Target
}

// NewJarRuntime creates a JarRuntime with the given target.
func NewJarRuntime(t target.Target) *JarRuntime {
	return &JarRuntime{target: t}
}

func (r *JarRuntime) Deploy(ctx context.Context, opts DeployOpts) error {
	installPath := filepath.Dir(opts.JarPath)

	// Create installation directory
	if _, err := r.target.Exec(ctx, "mkdir", "-p", installPath); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	// Download jar if URL provided and jar not yet present
	if opts.JarURL != "" {
		if err := r.downloadJar(ctx, opts.JarURL, opts.JarPath, opts.JarSHA256); err != nil {
			return fmt.Errorf("download jar: %w", err)
		}
	}

	// Write config file
	configPath := filepath.Join(installPath, "config.conf")
	if err := r.target.WriteFile(ctx, configPath, opts.ConfigData, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Write systemd unit file
	unitName := fmt.Sprintf("tron-%s.service", opts.Name)
	unitPath := filepath.Join("/etc/systemd/system", unitName)
	if err := r.target.WriteFile(ctx, unitPath, opts.SystemdData, 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	// Reload systemd and start service
	if _, err := r.target.Exec(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	// Set environment variables for the service
	for key, val := range opts.EnvVars {
		overridePath := fmt.Sprintf("/etc/systemd/system/%s.d", unitName)
		if _, err := r.target.Exec(ctx, "mkdir", "-p", overridePath); err != nil {
			return fmt.Errorf("create override dir: %w", err)
		}
		envOverride := fmt.Sprintf("[Service]\nEnvironment=%s=%s\n", key, val)
		envPath := filepath.Join(overridePath, "env.conf")
		if err := r.target.WriteFile(ctx, envPath, []byte(envOverride), 0600); err != nil {
			return fmt.Errorf("write env override: %w", err)
		}
	}

	if _, err := r.target.Exec(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload after env: %w", err)
	}

	if _, err := r.target.Exec(ctx, "systemctl", "enable", "--now", unitName); err != nil {
		return fmt.Errorf("enable + start service: %w", err)
	}

	return nil
}

func (r *JarRuntime) Start(ctx context.Context, name string) error {
	unitName := fmt.Sprintf("tron-%s.service", name)
	_, err := r.target.Exec(ctx, "systemctl", "start", unitName)
	return err
}

func (r *JarRuntime) Stop(ctx context.Context, name string) error {
	unitName := fmt.Sprintf("tron-%s.service", name)
	_, err := r.target.Exec(ctx, "systemctl", "stop", unitName)
	return err
}

func (r *JarRuntime) Remove(ctx context.Context, name string, purge bool) error {
	unitName := fmt.Sprintf("tron-%s.service", name)

	// Stop and disable
	r.target.Exec(ctx, "systemctl", "stop", unitName)
	r.target.Exec(ctx, "systemctl", "disable", unitName)

	// Remove unit file
	unitPath := filepath.Join("/etc/systemd/system", unitName)
	r.target.Exec(ctx, "rm", "-f", unitPath)

	// Remove override directory
	overridePath := fmt.Sprintf("/etc/systemd/system/%s.d", unitName)
	r.target.Exec(ctx, "rm", "-rf", overridePath)

	// Reload
	r.target.Exec(ctx, "systemctl", "daemon-reload")

	if purge {
		// Remove installation directory — will be determined from state
		// For now, we can't purge without knowing the install path
	}

	return nil
}

func (r *JarRuntime) Status(ctx context.Context, name string) (*NodeStatus, error) {
	unitName := fmt.Sprintf("tron-%s.service", name)
	out, err := r.target.Exec(ctx, "systemctl", "is-active", unitName)
	if err != nil {
		// systemctl exits non-zero for inactive/failed
		output := strings.TrimSpace(string(out))
		switch output {
		case "inactive":
			return &NodeStatus{Name: name, Status: "stopped"}, nil
		case "failed":
			return &NodeStatus{Name: name, Status: "error"}, nil
		default:
			return &NodeStatus{Name: name, Status: "unknown"}, nil
		}
	}

	return &NodeStatus{Name: name, Status: "running"}, nil
}

func (r *JarRuntime) Logs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error) {
	unitName := fmt.Sprintf("tron-%s.service", name)
	args := []string{"-u", unitName, "--no-pager"}
	if opts.Tail > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", opts.Tail))
	}
	if opts.Follow {
		args = append(args, "-f")
	}

	out, err := r.target.Exec(ctx, "journalctl", args...)
	if err != nil {
		return nil, fmt.Errorf("journalctl: %w", err)
	}

	return io.NopCloser(bytes.NewReader(out)), nil
}

// downloadJar downloads the jar file and verifies its SHA256 hash.
func (r *JarRuntime) downloadJar(ctx context.Context, url, destPath, expectedSHA256 string) error {
	// Check if jar already exists with correct hash
	if expectedSHA256 != "" {
		out, err := r.target.Exec(ctx, "sha256sum", destPath)
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) > 0 && fields[0] == expectedSHA256 {
				return nil // Already downloaded and verified
			}
		}
	}

	// Download
	if _, err := r.target.Exec(ctx, "curl", "-fSL", "-o", destPath, url); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}

	// Verify hash
	if expectedSHA256 != "" {
		out, err := r.target.Exec(ctx, "sha256sum", destPath)
		if err != nil {
			return fmt.Errorf("sha256sum: %w", err)
		}
		fields := strings.Fields(string(out))
		if len(fields) == 0 || fields[0] != expectedSHA256 {
			return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedSHA256, fields[0])
		}
	}

	return nil
}

// Ensure JarRuntime implements Runtime
var _ Runtime = (*JarRuntime)(nil)
