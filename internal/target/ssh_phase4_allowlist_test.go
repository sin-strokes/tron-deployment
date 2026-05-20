package target

import (
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/security"
)

// TestPhase4_SSHAllowlistCoverage is the regression guard for the
// review pass 1 bugs: every SSH-side command Phase 4's build
// pipeline issues MUST be on the security allowlist. Without this
// guard, an out-of-allowlist command added to PutFile /
// Sha256IfExists / CommandExists would silently fail at SSH time
// (since unit tests use fakeTarget, not the real ValidateCommand
// path).
//
// Adding a new command path to Phase 4 SSH flow → add to this list;
// CI will fail loudly if it's not also in the allowlist.
func TestPhase4_SSHAllowlistCoverage(t *testing.T) {
	required := []string{
		// PutFile's bash wrapper invokes these inside the session.
		// They're checked at the top of PutFile via
		// security.ValidateCommand as defense-in-depth before the
		// session even opens.
		"tee", // stream the file body
		"mv",  // atomic rename .tmp → final
		// PutFile cleanup path on cancel/failure.
		"rm",
		// Sha256IfExists.
		"sha256sum",
		// CommandExists (preflight FR-017 probe).
		"which",
	}
	for _, cmd := range required {
		t.Run(cmd, func(t *testing.T) {
			if err := security.ValidateCommand(cmd); err != nil {
				t.Errorf("%q must be on SSH allowlist for Phase 4: %v", cmd, err)
			}
		})
	}
}
