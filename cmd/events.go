package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tronprotocol/tron-deployment/internal/output"
)

// eventsCmd streams the audit log as a JSONL feed, optionally tailing.
//
// Test harnesses subscribe instead of polling status. Each line is the same
// auditEntry shape that mutating commands write, so consumers can correlate
// "stop happened at T → assert peer count drops by T+5s".
//
//	trond events                # dump current audit log, exit
//	trond events --follow       # tail forever, line-buffered
//	trond events --since 1h     # everything written in the last hour
var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Stream audit-log events (JSONL)",
	RunE:  runEvents,
}

var (
	eventsFollow bool
	eventsSince  time.Duration
)

func init() {
	eventsCmd.Flags().BoolVarP(&eventsFollow, "follow", "f", false, "Stream new events as they appear")
	eventsCmd.Flags().DurationVar(&eventsSince, "since", 0, "Only emit events newer than this (e.g. 1h, 5m)")
	rootCmd.AddCommand(eventsCmd)
}

func runEvents(cmd *cobra.Command, args []string) error {
	logPath := auditLogPath()

	// Open or create. Empty audit log + non-follow returns immediately.
	f, err := os.OpenFile(logPath, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return output.NewError("EVENTS_ERROR", output.ExitGeneralError, err.Error())
	}
	defer f.Close()

	since := time.Time{}
	if eventsSince > 0 {
		since = time.Now().Add(-eventsSince)
	}

	if err := emitExisting(f, since); err != nil {
		return err
	}

	if !eventsFollow {
		return nil
	}

	// Tail mode: poll the file with a short interval. We don't use fsnotify
	// to keep the dep tree small and to behave consistently across SSH /
	// remote-attached state dirs (where inotify wouldn't fire anyway).
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-tick.C:
			if err := emitNew(f); err != nil {
				return err
			}
		}
	}
}

// emitExisting reads from the current position to EOF, filtering by since.
func emitExisting(f *os.File, since time.Time) error {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if since.IsZero() {
			fmt.Println(string(line))
			continue
		}
		ts, ok := extractTimestamp(line)
		if !ok || !ts.Before(since) {
			fmt.Println(string(line))
		}
	}
	return scanner.Err()
}

// emitNew reads any bytes appended since the last call. Uses the file's
// current offset as the resume point, so callers must not seek between
// invocations.
func emitNew(f *os.File) error {
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			os.Stdout.WriteString(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// extractTimestamp grabs the timestamp field from a JSONL audit entry
// without doing a full unmarshal — events.log is hot path; a real test
// harness will parse the line itself.
func extractTimestamp(line []byte) (time.Time, bool) {
	const key = `"timestamp":"`
	idx := indexOf(line, key)
	if idx < 0 {
		return time.Time{}, false
	}
	rest := line[idx+len(key):]
	end := indexOf(rest, `"`)
	if end < 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, string(rest[:end]))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func indexOf(haystack []byte, needle string) int {
	n := []byte(needle)
	for i := 0; i+len(n) <= len(haystack); i++ {
		match := true
		for j := range n {
			if haystack[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
