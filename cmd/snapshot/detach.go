package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/output"
	"github.com/tronprotocol/tron-deployment/internal/paths"
	"github.com/tronprotocol/tron-deployment/internal/snapshot"
)

// spawnDetached re-execs the running trond binary with the same argv
// except --detach has been stripped, redirects stdout/stderr to a log
// file under <jobs>/<id>.log, and disowns the child so SIGHUP from the
// closing terminal doesn't reach it.
//
// We use os.StartProcess + SysProcAttr.Setsid (rather than syscall.ForkExec)
// so the child becomes its own session leader: it has no controlling
// terminal and is immune to terminal-driven signals. This is the same
// pattern shells use when you write `program &` followed by `disown`.
//
// The parent returns immediately, printing (or JSON-emitting) the job
// manifest so callers can `trond snapshot logs <id>` or `jobs`.
func spawnDetached(outputFmt string, src *snapshot.Source, backup, dest string) error {
	exe, err := os.Executable()
	if err != nil {
		return output.NewError("DETACH_ERROR", output.ExitGeneralError,
			"cannot resolve own binary path: "+err.Error())
	}
	// EvalSymlinks so a Homebrew/symlinked install doesn't restart via
	// a path the kernel may later evict. Failure is non-fatal — fall
	// back to the original.
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}

	jobsDir := paths.SnapshotJobs()
	if err := os.MkdirAll(jobsDir, 0o700); err != nil {
		return output.NewError("DETACH_ERROR", output.ExitGeneralError,
			"create jobs dir: "+err.Error())
	}
	id := snapshot.NewJobID(time.Now())
	logPath := filepath.Join(jobsDir, id+".log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return output.NewError("DETACH_ERROR", output.ExitGeneralError,
			"open log file: "+err.Error())
	}
	defer logFile.Close()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return output.NewError("DETACH_ERROR", output.ExitGeneralError,
			"open /dev/null: "+err.Error())
	}
	defer devNull.Close()

	// Build the child argv: copy os.Args, drop --detach (and the
	// possible `--detach=true` form). The child then runs in foreground
	// mode within its detached session and writes progress to the log.
	childArgs := stripDetach(os.Args)

	proc, err := os.StartProcess(exe, childArgs, &os.ProcAttr{
		Dir: ".",
		// Inherit env so TROND_STATE_DIR / HOME / proxy vars carry over.
		Env: os.Environ(),
		Files: []*os.File{
			devNull, // stdin
			logFile, // stdout
			logFile, // stderr
		},
		Sys: &syscall.SysProcAttr{
			Setsid: true, // become session leader → SIGHUP-immune
		},
	})
	if err != nil {
		return output.NewError("DETACH_ERROR", output.ExitGeneralError,
			"start child: "+err.Error())
	}

	job := snapshot.Job{
		ID:        id,
		PID:       proc.Pid,
		StartedAt: time.Now().UTC(),
		Args:      childArgs,
		LogPath:   logPath,
		DestDir:   dest,
		Backup:    backup,
		Network:   string(src.Network),
		Kind:      string(src.DBKind),
	}
	if err := snapshot.WriteJob(jobsDir, job); err != nil {
		// Don't kill the child — the download is still useful. Just
		// warn the user that they'll have to find it via PID.
		fmt.Fprintf(os.Stderr, "warning: could not persist job manifest: %v\n", err)
	}

	// Release the Process handle so the child becomes a true daemon
	// (inherited by init/launchd). Without Release(), the parent
	// would block on the OS until the child exits.
	if err := proc.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: release child: %v\n", err)
	}

	if outputFmt == "json" {
		return output.WriteJSON(os.Stdout, map[string]any{
			"job_id":   id,
			"pid":      job.PID,
			"log_path": logPath,
			"dest":     dest,
			"backup":   backup,
			"network":  job.Network,
			"kind":     job.Kind,
		})
	}
	fmt.Printf("Snapshot download started in background.\n")
	fmt.Printf("  job:  %s\n", id)
	fmt.Printf("  pid:  %d\n", job.PID)
	fmt.Printf("  log:  %s\n", logPath)
	fmt.Printf("Tail with: trond snapshot logs %s\n", id)
	fmt.Printf("Stop with: trond snapshot stop %s\n", id)
	return nil
}

// stripDetach returns argv with the --detach flag removed in any of its
// CLI-acceptable forms (--detach, --detach=true, -detach). Used so the
// re-execed child runs in foreground mode within its detached session.
func stripDetach(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == "--detach" || a == "-detach" {
			continue
		}
		if len(a) > 9 && (a[:9] == "--detach=" || a[:8] == "-detach=") {
			continue
		}
		out = append(out, a)
	}
	// Defensive: ensure we didn't strip away the trond invocation entirely.
	// If somehow the binary path got dropped, restore from os.Args[0].
	if len(out) == 0 {
		out = append(out, argv[0])
	}
	return slices.Clip(out)
}
