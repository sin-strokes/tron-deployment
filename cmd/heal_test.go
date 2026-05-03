package cmd

import (
	"testing"

	"github.com/tronprotocol/tron-deployment/internal/diagnosis"
)

// TestProposeHealAction pins the (check, current state) → action
// mapping for `trond auto-heal`. Every case here is a contract:
// adding a new mapping requires adding a case here so the table
// stays the source of truth.
func TestProposeHealAction(t *testing.T) {
	cases := []struct {
		name       string
		check      diagnosis.CheckResult
		nodeStatus string
		wantOK     bool
		wantAction string
	}{
		{
			name: "port-listening-fail-stopped-node-can-be-started",
			check: diagnosis.CheckResult{
				Name:   "port_listening",
				Status: diagnosis.StatusFail,
			},
			nodeStatus: "stopped",
			wantOK:     true,
			wantAction: "start",
		},
		{
			name: "port-listening-fail-running-node-no-auto-fix",
			check: diagnosis.CheckResult{
				Name:   "port_listening",
				Status: diagnosis.StatusFail,
			},
			// If state thinks the node is running but ports aren't
			// listening, auto-restart is risky (e.g. the container
			// may be in the middle of a long startup); surface to
			// human instead.
			nodeStatus: "running",
			wantOK:     false,
		},
		{
			name: "sync-progress-fail-no-auto-fix",
			check: diagnosis.CheckResult{
				Name:   "sync_progress",
				Status: diagnosis.StatusFail,
			},
			nodeStatus: "running",
			wantOK:     false,
		},
		{
			name: "peer-count-fail-no-auto-fix",
			check: diagnosis.CheckResult{
				Name:   "peer_count",
				Status: diagnosis.StatusFail,
			},
			nodeStatus: "running",
			wantOK:     false,
		},
		{
			name: "disk-space-fail-no-auto-fix",
			check: diagnosis.CheckResult{
				Name:   "disk_space",
				Status: diagnosis.StatusFail,
			},
			nodeStatus: "running",
			wantOK:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := proposeHealAction(tc.check, tc.nodeStatus)
			if ok != tc.wantOK {
				t.Errorf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && got.Action != tc.wantAction {
				t.Errorf("action: got %q, want %q", got.Action, tc.wantAction)
			}
		})
	}
}
