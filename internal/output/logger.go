package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// LogLevel represents log severity.
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Logger provides structured or text logging.
type Logger struct {
	w       io.Writer
	json    bool
	verbose bool
	quiet   bool
}

// NewLogger creates a logger. If jsonFmt is true, output is JSON lines.
func NewLogger(w io.Writer, jsonFmt, verbose, quiet bool) *Logger {
	return &Logger{w: w, json: jsonFmt, verbose: verbose, quiet: quiet}
}

// DefaultLogger returns a logger writing to stderr in text mode.
func DefaultLogger() *Logger {
	return &Logger{w: os.Stderr}
}

// Debug logs at debug level (only if verbose).
func (l *Logger) Debug(msg string, fields ...any) {
	if !l.verbose {
		return
	}
	l.log(LogDebug, msg, fields...)
}

// Info logs at info level (suppressed if quiet).
func (l *Logger) Info(msg string, fields ...any) {
	if l.quiet {
		return
	}
	l.log(LogInfo, msg, fields...)
}

// Warn logs at warn level.
func (l *Logger) Warn(msg string, fields ...any) {
	l.log(LogWarn, msg, fields...)
}

// Error logs at error level.
func (l *Logger) Error(msg string, fields ...any) {
	l.log(LogError, msg, fields...)
}

func (l *Logger) log(level LogLevel, msg string, fields ...any) {
	if l.json {
		entry := map[string]any{
			"time":  time.Now().UTC().Format(time.RFC3339),
			"level": string(level),
			"msg":   msg,
		}
		for i := 0; i+1 < len(fields); i += 2 {
			key, ok := fields[i].(string)
			if !ok {
				continue
			}
			entry[key] = fields[i+1]
		}
		data, _ := json.Marshal(entry)
		fmt.Fprintln(l.w, string(data))
	} else {
		prefix := ""
		switch level {
		case LogDebug:
			prefix = "[DEBUG] "
		case LogWarn:
			prefix = "[WARN]  "
		case LogError:
			prefix = "[ERROR] "
		}
		if len(fields) == 0 {
			fmt.Fprintf(l.w, "%s%s\n", prefix, msg)
		} else {
			fmt.Fprintf(l.w, "%s%s", prefix, msg)
			for i := 0; i+1 < len(fields); i += 2 {
				fmt.Fprintf(l.w, " %v=%v", fields[i], fields[i+1])
			}
			fmt.Fprintln(l.w)
		}
	}
}
