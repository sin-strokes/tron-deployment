//go:build e2e

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_StateDirPriority pins down the base-dir resolution order
// documented in internal/paths/paths.go:
//
//  1. --state-dir CLI flag (highest)
//  2. TROND_STATE_DIR env var
//  3. ~/.trond default (lowest)
//
// Without this test, a future refactor that flips the priority would
// silently break agents that rely on TROND_STATE_DIR for sandboxing
// or CI runners that pass --state-dir to isolate runs.
//
// We exercise priority via `trond doctor`'s "state dir" check, which
// echoes whichever path was resolved. No Docker required.
func TestE2E_StateDirPriority(t *testing.T) {
	flagDir := t.TempDir()
	envDir := t.TempDir()

	cases := []struct {
		name     string
		args     []string
		extraEnv []string
		wantDir  string
		wantNot  []string
	}{
		{
			name: "flag-beats-env",
			args: []string{"--state-dir", flagDir},
			extraEnv: []string{
				"TROND_STATE_DIR=" + envDir,
			},
			wantDir: flagDir,
			wantNot: []string{envDir},
		},
		{
			name:     "env-when-no-flag",
			args:     nil,
			extraEnv: []string{"TROND_STATE_DIR=" + envDir},
			wantDir:  envDir,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := append([]string{}, os.Environ()...)
			// Strip any inherited TROND_STATE_DIR from the process —
			// the test must control it explicitly per case.
			env = stripPrefix(env, "TROND_STATE_DIR=")
			env = append(env, tc.extraEnv...)

			args := append([]string{}, tc.args...)
			args = append(args, "doctor", "--output", "json")

			out := runTrondCtx(ctx, t, env, args...)
			body := string(out)

			if !strings.Contains(body, tc.wantDir) {
				t.Errorf("doctor output should mention resolved dir %q, got:\n%s",
					tc.wantDir, body)
			}
			for _, unwanted := range tc.wantNot {
				if strings.Contains(body, unwanted) {
					t.Errorf("doctor output should NOT mention %q (lower-priority dir), got:\n%s",
						unwanted, body)
				}
			}
		})
	}

	// Sanity: --state-dir actually creates files in flagDir, not env.
	t.Run("flag-writes-into-flagdir", func(t *testing.T) {
		env := stripPrefix(append([]string{}, os.Environ()...), "TROND_STATE_DIR=")
		env = append(env, "TROND_STATE_DIR="+envDir)
		dir := t.TempDir()
		// `trond list` is the cheapest write-state command — at minimum
		// it touches state.json by reading it (creating the dir tree
		// implicitly is enough for the test).
		_ = runTrondCtx(ctx, t, env, "--state-dir", dir, "list", "--output", "json")
		// dir should now exist (paths.BaseDir creates if needed via
		// the first state op). envDir should not have grown a state.json
		// from this invocation because we passed --state-dir.
		if _, err := os.Stat(filepath.Join(envDir, "state.json")); err == nil {
			t.Errorf("envDir state.json should not exist when --state-dir overrides it")
		}
	})
}

// stripPrefix returns env without entries that begin with prefix.
func stripPrefix(env []string, prefix string) []string {
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return out
}
