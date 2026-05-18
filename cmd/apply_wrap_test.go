package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/output"
)

// TestWrapApplyError_PassesThroughStructuredError is the Phase 2
// review-pass-2 regression guard: errors that already carry an
// error_code (BUILD_FAILED, INVALID_SOURCE, VALIDATION_ERROR, …)
// from internal/build or internal/apply must propagate to the user
// unchanged. Wrapping them in DEPLOY_ERROR would strip the
// specificity agents rely on for retry / suggest-fix logic.
func TestWrapApplyError_PassesThroughStructuredError(t *testing.T) {
	cases := []struct {
		name     string
		code     string
		exitCode int
	}{
		{"BUILD_FAILED", "BUILD_FAILED", output.ExitGeneralError},
		{"INVALID_SOURCE", "INVALID_SOURCE", output.ExitValidationError},
		{"BUILD_CANCELLED", "BUILD_CANCELLED", 130},
		{"VALIDATION_ERROR", "VALIDATION_ERROR", output.ExitValidationError},
		{"INVALID_ARTIFACT", "INVALID_ARTIFACT", output.ExitGeneralError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := output.NewError(tc.code, tc.exitCode, "test message")
			got := wrapApplyError(in)

			var se *output.StructuredError
			if !errors.As(got, &se) {
				t.Fatalf("expected *StructuredError; got %T", got)
			}
			if se.Code != tc.code {
				t.Errorf("error_code = %q; want %q (wrap stripped specificity)", se.Code, tc.code)
			}
			if se.ExitCode != tc.exitCode {
				t.Errorf("exit_code = %d; want %d", se.ExitCode, tc.exitCode)
			}
		})
	}
}

// TestWrapApplyError_WrapsGenericError covers the OTHER half: a
// raw error (e.g. fmt.Errorf from the deploy plumbing) becomes a
// DEPLOY_ERROR envelope so the user still sees structured output.
func TestWrapApplyError_WrapsGenericError(t *testing.T) {
	in := fmt.Errorf("docker compose up: connection refused")
	got := wrapApplyError(in)

	var se *output.StructuredError
	if !errors.As(got, &se) {
		t.Fatalf("expected *StructuredError; got %T (%v)", got, got)
	}
	if se.Code != "DEPLOY_ERROR" {
		t.Errorf("error_code = %q; want DEPLOY_ERROR", se.Code)
	}
	if se.ExitCode != output.ExitGeneralError {
		t.Errorf("exit_code = %d; want %d", se.ExitCode, output.ExitGeneralError)
	}
	if len(se.Suggestions) == 0 {
		t.Error("DEPLOY_ERROR should carry remediation suggestions")
	}
}

// TestWrapApplyError_NilPassthrough — happy path: nil in, nil out.
func TestWrapApplyError_NilPassthrough(t *testing.T) {
	if got := wrapApplyError(nil); got != nil {
		t.Errorf("nil should pass through; got %v", got)
	}
}
