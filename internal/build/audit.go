package build

import (
	"time"

	"github.com/tronprotocol/tron-deployment/internal/security"
)

// AuditPhase represents where in the build lifecycle we are. Per
// FR-023 we append an `in_progress` event at start, then write a
// terminal event (success / failed / cancelled) on completion. A
// crashed mid-build leaves the `in_progress` entry visible to
// `trond events`, surfacing the forensic signal.
type AuditPhase string

const (
	PhaseInProgress AuditPhase = "in_progress"
	PhaseSuccess    AuditPhase = "success"
	PhaseFailed     AuditPhase = "failed"
	PhaseCancelled  AuditPhase = "cancelled"
)

// AppendAuditEvent writes one build-related row to the audit log.
//
// We deliberately reuse the existing security.AuditEntry shape (the
// same envelope `apply`, `start`, `stop` use) so downstream tooling —
// `trond events`, MCP resources, the JSON contract — doesn't need a
// new code path. The build-specific fields ride in the existing
// ErrorCode/Result columns.
func AppendAuditEvent(phase AuditPhase, cacheKey, errorCode string, startedAt time.Time) error {
	log, err := security.NewAuditLog("")
	if err != nil {
		return err
	}
	entry := security.AuditEntry{
		Timestamp:  time.Now().UTC(),
		Command:    "build",
		Target:     "local", // build target is always local in v1
		IntentHash: cacheKey,
		Result:     string(phase),
		ErrorCode:  errorCode,
	}
	if phase != PhaseInProgress {
		entry.DurationMs = time.Since(startedAt).Milliseconds()
	}
	return log.Write(entry)
}
