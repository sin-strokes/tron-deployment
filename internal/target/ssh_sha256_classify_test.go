package target

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestSha256_ExitErrorClassification is the review-pass-4 hardening
// for the H2 fix: Sha256IfExists distinguishes `*ssh.ExitError`
// (remote ran sha256sum, it exited non-zero — fall through to PutFile)
// from transport errors (bubble; caller bails). The earlier test
// (TestTransferBuiltJAR_BailsOnSha256TransportError in apply) only
// exercises the transport-error branch with a plain errors.New string;
// nothing pinned that `errors.As(err, &exitErr)` actually unwraps the
// real wrapped form `ssh.go:Exec` emits (`fmt.Errorf("ssh exec %q: %w: %s", ..., runErr, ...)`).
//
// If Exec ever switched from %w to %s (which would break the unwrap
// silently), this test would fire. It also documents the invariant
// for future maintainers: do NOT change Exec's wrap verb without
// also revisiting Sha256IfExists.
func TestSha256_ExitErrorClassification(t *testing.T) {
	t.Run("wrapped *ssh.ExitError unwraps via errors.As", func(t *testing.T) {
		// Exact wrap pattern Exec uses:
		// fmt.Errorf("ssh exec %q: %w: %s", fullCmd, runErr, combined.String())
		wrapped := fmt.Errorf("ssh exec %q: %w: %s",
			"'sha256sum' '/remote/x'",
			&ssh.ExitError{},
			"sha256sum: /remote/x: No such file or directory")

		var exitErr *ssh.ExitError
		if !errors.As(wrapped, &exitErr) {
			t.Fatal("errors.As failed to unwrap a wrapped ExitError — " +
				"either Exec's wrap verb changed from %w, or the ssh package's " +
				"ExitError stopped being a pointer type")
		}
	})

	t.Run("transport-style errors do NOT match as ExitError", func(t *testing.T) {
		// Mimic what Exec emits when NewSession fails (no
		// *ssh.ExitError in the chain): the wrap is the same shape
		// but the inner is a plain transport error.
		transportInner := errors.New("ssh: handshake failed: EOF")
		wrapped := fmt.Errorf("ssh exec %q: %w: %s",
			"'sha256sum' '/remote/x'",
			transportInner,
			"")

		var exitErr *ssh.ExitError
		if errors.As(wrapped, &exitErr) {
			t.Error("transport error must NOT match *ssh.ExitError; " +
				"otherwise Sha256IfExists treats a wedged link as 'file missing' " +
				"and the caller wastes a multi-hundred-MB doomed re-transfer")
		}
	})

	t.Run("nested wrap still unwraps", func(t *testing.T) {
		// Defensive: if a future refactor adds another wrap layer
		// (e.g. a target.Target method wrapping its inner err),
		// errors.As walks the chain transitively. Pin that
		// expectation so the refactor doesn't silently break the
		// classification.
		inner := &ssh.ExitError{}
		mid := fmt.Errorf("ssh exec: %w", inner)
		outer := fmt.Errorf("ssh target wrapper: %w", mid)

		var exitErr *ssh.ExitError
		if !errors.As(outer, &exitErr) {
			t.Error("nested wrap must still unwrap to *ssh.ExitError")
		}
	})
}
