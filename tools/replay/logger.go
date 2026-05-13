package main

import (
	"encoding/json"
	"os"
)

// jsonlLogger writes each record as one JSON line to a file in append mode.
//
// Deliberately kept simple (no buffering) so `tail -F` shows progress in
// real time.
type jsonlLogger struct {
	f *os.File
}

func openJsonlLogger(path string) (*jsonlLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &jsonlLogger{f: f}, nil
}

func (l *jsonlLogger) writeRecord(rec map[string]any) {
	data, _ := json.Marshal(rec)
	_, _ = l.f.Write(data)
	_, _ = l.f.Write([]byte("\n"))
}

func (l *jsonlLogger) Close() { _ = l.f.Close() }
