package security

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAuditLog_TimestampUTC pins the contract that audit log entries
// always emit timestamps in UTC. SIEM ingest pipelines often correlate
// across hosts; if some entries were in local time and some in UTC,
// timeline reconstruction would be off by hours.
func TestAuditLog_TimestampUTC(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	al, err := NewAuditLog(logPath)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	if err := al.Write(AuditEntry{
		Command: "test", Result: "success", Target: "local",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	scanner := bytes.NewReader(bytes.TrimSpace(body))
	var entry AuditEntry
	if err := json.NewDecoder(scanner).Decode(&entry); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// time.Time.MarshalJSON uses RFC3339Nano. UTC entries end in
	// "Z"; local-time entries end in a numeric offset like "+08:00".
	stamp, err := json.Marshal(entry.Timestamp)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	stampStr := strings.Trim(string(stamp), `"`)
	if !strings.HasSuffix(stampStr, "Z") {
		t.Errorf("timestamp not UTC (must end in 'Z'): %s", stampStr)
	}

	// Sanity: timezone offset is zero relative to UTC.
	loc := entry.Timestamp.Location()
	if loc != time.UTC && loc.String() != "UTC" {
		t.Errorf("timestamp location: want UTC, got %s", loc)
	}
}
