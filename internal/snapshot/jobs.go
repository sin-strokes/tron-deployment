package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Job records a detached `snapshot download` invocation: enough metadata
// to find the running process, tail its log, and tell whether it's still
// alive. Persisted as <jobsDir>/<ID>.json next to <jobsDir>/<ID>.log.
//
// We deliberately don't store progress here — that would mean periodic
// rewrites racing with the worker. Callers who want progress read the
// log file directly (which is line-oriented and append-only).
type Job struct {
	ID        string    `json:"id"` // e.g. "20260427-153012-abcd"
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Args      []string  `json:"args"` // exact argv (sans --detach) used to launch
	LogPath   string    `json:"log_path"`
	DestDir   string    `json:"dest_dir"`
	Backup    string    `json:"backup"`
	Network   string    `json:"network"`
	Kind      string    `json:"kind"`
}

// JobStatus is the live state of a Job, computed from the on-disk
// manifest plus a kill(0) probe of the PID.
type JobStatus struct {
	Job
	Running    bool      `json:"running"`
	Finished   bool      `json:"finished"`            // process gone, log present
	ExitNote   string    `json:"exit_note,omitempty"` // last log line if not running
	LogSize    int64     `json:"log_size_bytes"`
	LogModTime time.Time `json:"log_mod_time"`
}

// WriteJob persists a Job manifest to disk. The caller chose ID and is
// expected to keep .log next to .json with the same basename.
func WriteJob(dir string, j Job) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ensure jobs dir: %w", err)
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, j.ID+".json")
	return os.WriteFile(path, data, 0o600)
}

// ReadJob loads a single manifest by ID.
func ReadJob(dir, id string) (*Job, error) {
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobs scans the jobs directory and returns every recorded job,
// newest-first by StartedAt. Missing dir returns ([]).
func ListJobs(dir string) ([]Job, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Job, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		j, err := ReadJob(dir, id)
		if err != nil {
			continue // skip unreadable entries; don't fail the listing
		}
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// RemoveJob deletes the manifest and its log. Caller should have already
// confirmed the process isn't running.
func RemoveJob(dir, id string) error {
	manifestErr := os.Remove(filepath.Join(dir, id+".json"))
	logErr := os.Remove(filepath.Join(dir, id+".log"))
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return manifestErr
	}
	if logErr != nil && !errors.Is(logErr, os.ErrNotExist) {
		return logErr
	}
	return nil
}

// IsRunning probes whether a PID is still alive via kill(0). The
// canonical Unix idiom has three outcomes:
//
//	nil   → the process exists and we can signal it (alive)
//	EPERM → the process exists but a different user owns it (alive,
//	        but a child of root or a sandboxed process — happens often
//	        on macOS where launchd-managed daemons reparent under PID 1)
//	ESRCH → no such process (dead)
//
// Both nil and EPERM mean "alive". Treating EPERM as "dead" misclassifies
// detached snapshot downloads that have legitimately reparented to
// init/launchd after the trond invocation that spawned them exited.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; we have to actually signal.
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// Status fills in live fields by stat'ing the log file and probing the
// PID. The on-disk manifest is treated as immutable.
func Status(dir string, j Job) JobStatus {
	st := JobStatus{Job: j}
	info, err := os.Stat(j.LogPath)
	if err == nil {
		st.LogSize = info.Size()
		st.LogModTime = info.ModTime()
	}
	st.Running = IsRunning(j.PID)
	if !st.Running {
		// Best-effort: read the last line of the log so the user knows
		// whether it finished cleanly.
		if note := tailLastLine(j.LogPath, 256); note != "" {
			st.ExitNote = note
			st.Finished = true
		}
	}
	return st
}

// tailLastLine reads up to maxBytes from the end of a file and returns
// the final non-empty line. Used by Status() for a one-glance summary.
func tailLastLine(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(info.Size()-maxBytes, 0)
	if _, err := f.Seek(off, 0); err != nil {
		return ""
	}
	buf := make([]byte, info.Size()-off)
	if _, err := f.Read(buf); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			return l
		}
	}
	return ""
}

// NewJobID returns a sortable, collision-resistant job id of the form
// "20060102-150405-xxxx". The 4-char suffix is derived from
// time.Now().UnixNano() bits to avoid pulling in a fresh randomness
// source — it's collision-resistant within the same second, which is
// all we need for a single-host CLI.
func NewJobID(now time.Time) string {
	nano := now.UnixNano()
	suffix := fmt.Sprintf("%04x", nano&0xFFFF)
	return now.UTC().Format("20060102-150405") + "-" + suffix
}
