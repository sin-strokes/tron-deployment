package target

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// LocalTarget executes commands and file operations on the local machine.
type LocalTarget struct{}

// NewLocalTarget creates a new LocalTarget.
func NewLocalTarget() *LocalTarget {
	return &LocalTarget{}
}

func (t *LocalTarget) Exec(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("exec %s: %w: %s", cmd, err, string(out))
	}
	return out, nil
}

func (t *LocalTarget) Upload(_ context.Context, localPath, remotePath string) error {
	// Local target: just copy the file
	return copyFile(localPath, remotePath)
}

func (t *LocalTarget) Download(_ context.Context, remotePath, localPath string) error {
	return copyFile(remotePath, localPath)
}

func (t *LocalTarget) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (t *LocalTarget) WriteFile(_ context.Context, path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (t *LocalTarget) DiskFree(ctx context.Context, path string) (uint64, error) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "df"
		args = []string{"-k", path}
	default: // linux
		cmd = "df"
		args = []string{"--output=avail", "-k", path}
	}

	out, err := t.Exec(ctx, cmd, args...)
	if err != nil {
		return 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output")
	}

	// Parse the available KB from the last line
	fields := strings.Fields(lines[len(lines)-1])
	var availField string
	if runtime.GOOS == "darwin" {
		if len(fields) < 4 {
			return 0, fmt.Errorf("unexpected df output format")
		}
		availField = fields[3] // Available column on macOS
	} else {
		availField = fields[0]
	}

	kb, err := strconv.ParseUint(strings.TrimSpace(availField), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse disk free: %w", err)
	}

	return kb * 1024, nil // Convert KB to bytes
}

func (t *LocalTarget) MemTotal(ctx context.Context) (uint64, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := t.Exec(ctx, "sysctl", "-n", "hw.memsize")
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	default: // linux
		data, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, fmt.Errorf("read meminfo: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parse memtotal: %w", err)
				}
				return kb * 1024, nil
			}
		}
		return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
	}
}

func (t *LocalTarget) String() string {
	return "local"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
