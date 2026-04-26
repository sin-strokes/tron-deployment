package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/state"
)

// doctorCmd inspects the local trond installation and prints a quick
// "is anything wrong" summary. Designed to be the first command someone
// pastes into an issue: it answers "what version, what state, are the
// dependencies trond expects actually present, is anything stale".
//
// Probes are deliberately cheap and don't hit the network unless the
// user opts in via --check-update.
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run self-checks on the local trond install",
	Long: `Print a status report covering trond's version, state directory,
docker availability, file permissions, and (optionally) whether a newer
release exists.

Suggested first command to paste into an issue.`,
	RunE: runDoctor,
}

var doctorCheckUpdate bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorCheckUpdate, "check-update", false, "Also query GitHub for the latest release (network)")
	rootCmd.AddCommand(doctorCmd)
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // pass | warn | fail
	Message string `json:"message"`
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")
	checks := []doctorCheck{
		checkBinaryVersion(),
		checkStateDir(),
		checkStateFile(),
		checkLockFile(),
		checkAuditLog(),
		checkDockerCLI(cmd.Context()),
	}
	if doctorCheckUpdate {
		checks = append(checks, checkUpdate(cmd.Context()))
	}

	overall := "pass"
	for _, c := range checks {
		if c.Status == "fail" {
			overall = "fail"
		} else if c.Status == "warn" && overall != "fail" {
			overall = "warn"
		}
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"overall": overall,
			"checks":  checks,
		})
	}

	for _, c := range checks {
		icon := "✓"
		switch c.Status {
		case "warn":
			icon = "⚠"
		case "fail":
			icon = "✗"
		}
		fmt.Printf("%s %-22s %s\n", icon, c.Name, c.Message)
	}
	fmt.Printf("\nOverall: %s\n", overall)
	if overall == "fail" {
		return output.NewError("DOCTOR_FAIL", output.ExitGeneralError, "one or more doctor checks failed")
	}
	return nil
}

func checkBinaryVersion() doctorCheck {
	return doctorCheck{
		Name:    "trond version",
		Status:  "pass",
		Message: fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildTime),
	}
}

func checkStateDir() doctorCheck {
	dir := paths.BaseDir()
	info, err := os.Stat(dir)
	if err != nil {
		return doctorCheck{Name: "state dir", Status: "warn",
			Message: dir + " does not exist yet (will be created on first apply)"}
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return doctorCheck{Name: "state dir", Status: "warn",
			Message: fmt.Sprintf("%s perms %o are too open; expected 0700", dir, mode)}
	}
	return doctorCheck{Name: "state dir", Status: "pass", Message: dir + " (mode 0700)"}
}

func checkStateFile() doctorCheck {
	p := paths.State()
	info, err := os.Stat(p)
	if os.IsNotExist(err) {
		return doctorCheck{Name: "state.json", Status: "pass", Message: "absent (no nodes managed yet)"}
	}
	if err != nil {
		return doctorCheck{Name: "state.json", Status: "fail", Message: err.Error()}
	}
	store, err := state.NewStore(p)
	if err != nil {
		return doctorCheck{Name: "state.json", Status: "fail", Message: err.Error()}
	}
	st, err := store.Load()
	if err != nil {
		return doctorCheck{Name: "state.json", Status: "fail", Message: "parse: " + err.Error()}
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return doctorCheck{Name: "state.json", Status: "warn",
			Message: fmt.Sprintf("perms %o; expected 0600", mode)}
	}
	return doctorCheck{Name: "state.json", Status: "pass",
		Message: fmt.Sprintf("%d managed node(s)", len(st.Nodes))}
}

func checkLockFile() doctorCheck {
	lock := filepath.Join(paths.BaseDir(), "state.lock")
	info, err := os.Stat(lock)
	if os.IsNotExist(err) {
		return doctorCheck{Name: "state.lock", Status: "pass", Message: "absent (no concurrent operation)"}
	}
	if err != nil {
		return doctorCheck{Name: "state.lock", Status: "warn", Message: err.Error()}
	}
	// We can't tell if the holder is alive without OS-specific calls;
	// surface age instead so a stale lock from a crash stands out.
	age := time.Since(info.ModTime()).Round(time.Second)
	if age > time.Hour {
		return doctorCheck{Name: "state.lock", Status: "warn",
			Message: fmt.Sprintf("present, age %s — possibly stale (manual delete may be needed)", age)}
	}
	return doctorCheck{Name: "state.lock", Status: "pass",
		Message: fmt.Sprintf("present, age %s", age)}
}

func checkAuditLog() doctorCheck {
	p := paths.AuditLog()
	info, err := os.Stat(p)
	if os.IsNotExist(err) {
		return doctorCheck{Name: "audit.log", Status: "pass", Message: "absent (no events yet)"}
	}
	if err != nil {
		return doctorCheck{Name: "audit.log", Status: "warn", Message: err.Error()}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return doctorCheck{Name: "audit.log", Status: "warn",
			Message: fmt.Sprintf("perms %o; expected 0600", info.Mode().Perm())}
	}
	return doctorCheck{Name: "audit.log", Status: "pass",
		Message: fmt.Sprintf("%d bytes", info.Size())}
}

func checkDockerCLI(ctx context.Context) doctorCheck {
	if _, err := exec.LookPath("docker"); err != nil {
		return doctorCheck{Name: "docker CLI", Status: "warn",
			Message: "not in PATH (only matters for runtime: docker)"}
	}
	c := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Client.Version}}")
	out, err := c.Output()
	if err != nil {
		return doctorCheck{Name: "docker CLI", Status: "warn",
			Message: "found but `docker version` failed: " + strings.TrimSpace(err.Error())}
	}
	return doctorCheck{Name: "docker CLI", Status: "pass",
		Message: "v" + strings.TrimSpace(string(out))}
}

func checkUpdate(ctx context.Context) doctorCheck {
	latest, err := fetchLatestRelease(ctx)
	if err != nil {
		return doctorCheck{Name: "update check", Status: "warn",
			Message: "could not query GitHub: " + err.Error()}
	}
	if latest == "" {
		return doctorCheck{Name: "update check", Status: "pass",
			Message: "no releases published"}
	}
	if isNewer(latest, version) {
		return doctorCheck{Name: "update check", Status: "warn",
			Message: fmt.Sprintf("update available: %s → %s", version, latest)}
	}
	return doctorCheck{Name: "update check", Status: "pass",
		Message: "up to date (" + latest + ")"}
}

// _ catches the unused http import when --check-update is the only consumer
// in some build scenarios; keeps refactors safe.
var _ = http.StatusOK
