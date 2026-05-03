//go:build e2e

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestE2E_ExitCodes pins down the exit-code contract documented in
// AGENTS.md "Exit codes" — agents read these numbers to decide
// whether to retry, escalate, or stop. The table mirrors that doc:
//
//	0  success
//	1  general (NODE_NOT_FOUND, EXEC_ERROR, WAIT_TIMEOUT, ...)
//	2  validation error
//	3  target unreachable
//	4  preflight failure  -- not exercised here (would need a
//	                        host with no docker; covered by unit
//	                        tests in cmd/preflight.go)
//	10 HUMAN_REQUIRED — destructive op without --confirm/--auto-approve
//
// Each case asserts BOTH the OS exit code AND the JSON `exit_code`
// field, since the latter is part of the public error envelope and
// agents may key off it instead of `os.ProcessState.ExitCode()`.
//
// No Docker required.
func TestE2E_ExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantExit int
		// wantCode is the error_code field in the JSON envelope.
		// Empty string means "skip this assertion" (success path
		// emits no envelope).
		wantCode string
	}{
		// 0 — success: a known no-op read-side command.
		{
			name:     "success-version",
			args:     []string{"version"},
			wantExit: 0,
		},
		{
			name:     "success-list-empty",
			args:     []string{"list"},
			wantExit: 0,
		},

		// 2 — validation: intent file path doesn't exist.
		{
			name:     "validation-missing-intent",
			args:     []string{"config", "validate", "/no/such/intent.yaml"},
			wantExit: 2,
			wantCode: "VALIDATION_ERROR",
		},
		// 2 — validation: inspect with no selector flag.
		{
			name:     "validation-inspect-no-selector",
			args:     []string{"inspect"},
			wantExit: 2,
			wantCode: "VALIDATION_ERROR",
		},

		// 1 — general: status of a node that doesn't exist.
		{
			name:     "general-node-not-found",
			args:     []string{"status", "definitely-not-deployed"},
			wantExit: 1,
			wantCode: "NODE_NOT_FOUND",
		},

		// 10 — human required: remove without --confirm.
		{
			name:     "human-remove-no-confirm",
			args:     []string{"remove", "anything"},
			wantExit: 10,
			wantCode: "HUMAN_REQUIRED",
		},
		// 10 — human required: network destroy without --confirm.
		{
			name:     "human-network-destroy-no-confirm",
			args:     []string{"network", "destroy"},
			wantExit: 10,
			wantCode: "HUMAN_REQUIRED",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, env := e2eEnv(t)
			args := append([]string{}, tc.args...)
			args = append(args, "--output", "json")

			out, err := runTrondAllowFail(ctx, t, env, args...)

			gotExit := exitCodeOf(err)
			if gotExit != tc.wantExit {
				t.Errorf("exit code: got %d, want %d\nargs: %v\noutput:\n%s",
					gotExit, tc.wantExit, tc.args, out)
			}

			if tc.wantCode == "" {
				return
			}
			var env2 struct {
				ErrorCode string `json:"error_code"`
				ExitCode  int    `json:"exit_code"`
			}
			if jsonErr := json.Unmarshal(out, &env2); jsonErr != nil {
				t.Fatalf("error envelope not JSON: %v\noutput:\n%s", jsonErr, out)
			}
			if env2.ErrorCode != tc.wantCode {
				t.Errorf("error_code: got %q, want %q\noutput:\n%s",
					env2.ErrorCode, tc.wantCode, out)
			}
			if env2.ExitCode != tc.wantExit {
				t.Errorf("envelope exit_code: got %d, want %d (must match OS exit code %d)\noutput:\n%s",
					env2.ExitCode, tc.wantExit, gotExit, out)
			}
		})
	}
}

// exitCodeOf normalises the exec.ExitError extraction. Returns 0 on
// success, the CLI's exit code on a failed run, and -1 for
// transport-level failures (test config issue, not a CLI exit the
// test cares about).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
