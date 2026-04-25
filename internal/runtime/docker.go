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

// DockerRuntime manages nodes via docker compose.
type DockerRuntime struct {
	target  target.Target
	workDir string // Directory containing compose files
}

// NewDockerRuntime creates a DockerRuntime with the given target and working directory.
func NewDockerRuntime(t target.Target, workDir string) *DockerRuntime {
	return &DockerRuntime{
		target:  t,
		workDir: workDir,
	}
}

func (r *DockerRuntime) Deploy(ctx context.Context, opts DeployOpts) error {
	dir := filepath.Join(r.workDir, opts.Name)

	// Create deployment directory
	if _, err := r.target.Exec(ctx, "mkdir", "-p", dir); err != nil {
		return fmt.Errorf("create deploy dir: %w", err)
	}

	// Write config file
	configPath := filepath.Join(dir, opts.Name+".conf")
	if err := r.target.WriteFile(ctx, configPath, opts.ConfigData, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Write compose file
	composePath := filepath.Join(dir, "docker-compose.yaml")
	if err := r.target.WriteFile(ctx, composePath, opts.ComposeData, 0644); err != nil {
		return fmt.Errorf("write compose: %w", err)
	}

	// Docker compose up
	args := []string{"compose", "-f", composePath, "-p", opts.Name, "up", "-d"}
	if _, err := r.target.Exec(ctx, "docker", args...); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}

	return nil
}

func (r *DockerRuntime) Start(ctx context.Context, name string) error {
	composePath := filepath.Join(r.workDir, name, "docker-compose.yaml")
	_, err := r.target.Exec(ctx, "docker", "compose", "-f", composePath, "-p", name, "start")
	return err
}

func (r *DockerRuntime) Stop(ctx context.Context, name string) error {
	composePath := filepath.Join(r.workDir, name, "docker-compose.yaml")
	_, err := r.target.Exec(ctx, "docker", "compose", "-f", composePath, "-p", name, "stop")
	return err
}

func (r *DockerRuntime) Remove(ctx context.Context, name string, purge bool) error {
	composePath := filepath.Join(r.workDir, name, "docker-compose.yaml")
	args := []string{"compose", "-f", composePath, "-p", name, "down"}
	if purge {
		args = append(args, "-v") // Remove volumes too
	}
	_, err := r.target.Exec(ctx, "docker", args...)
	if err != nil {
		return fmt.Errorf("docker compose down: %w", err)
	}

	if purge {
		// Remove the deploy directory
		dir := filepath.Join(r.workDir, name)
		if _, err := r.target.Exec(ctx, "rm", "-rf", dir); err != nil {
			return fmt.Errorf("remove deploy dir: %w", err)
		}
	}

	return nil
}

func (r *DockerRuntime) Status(ctx context.Context, name string) (*NodeStatus, error) {
	composePath := filepath.Join(r.workDir, name, "docker-compose.yaml")
	out, err := r.target.Exec(ctx, "docker", "compose", "-f", composePath, "-p", name, "ps", "--format", "json")
	if err != nil {
		return &NodeStatus{Name: name, Status: "unknown"}, nil
	}

	output := strings.TrimSpace(string(out))
	if output == "" || output == "[]" {
		return &NodeStatus{Name: name, Status: "stopped"}, nil
	}

	// Simple status detection from docker compose ps output
	status := "unknown"
	if strings.Contains(output, "running") {
		status = "running"
	} else if strings.Contains(output, "exited") {
		status = "stopped"
	}

	return &NodeStatus{Name: name, Status: status}, nil
}

func (r *DockerRuntime) Logs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error) {
	composePath := filepath.Join(r.workDir, name, "docker-compose.yaml")
	args := []string{"compose", "-f", composePath, "-p", name, "logs"}
	if opts.Tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", opts.Tail))
	}
	if opts.Follow {
		args = append(args, "-f")
	}

	out, err := r.target.Exec(ctx, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker compose logs: %w", err)
	}

	return io.NopCloser(bytes.NewReader(out)), nil
}

// WorkDir returns the path to the deployment work directory for this node.
func (r *DockerRuntime) WorkDir(name string) string {
	return filepath.Join(r.workDir, name)
}

// Ensure DockerRuntime implements Runtime
var _ Runtime = (*DockerRuntime)(nil)
