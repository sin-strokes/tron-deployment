package build

import (
	"strings"
	"testing"
)

// TestMatrixWarning enforces the java-tron compat matrix soft-warn:
// in-matrix combos return empty, out-of-matrix combos return a
// human-readable message that includes both the offending platform
// and the expected JDK so the user knows what trond expected.
func TestMatrixWarning(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		jdk      string
		wantWarn bool
	}{
		{"amd64+8 in matrix", "linux/amd64", "8", false},
		{"arm64+17 in matrix", "linux/arm64", "17", false},
		{"amd64+17 out", "linux/amd64", "17", true},
		{"arm64+8 out", "linux/arm64", "8", true},
		{"amd64+11 out", "linux/amd64", "11", true},
		{"arm64+21 out", "linux/arm64", "21", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matrixWarning(tc.platform, tc.jdk)
			gotWarn := got != ""
			if gotWarn != tc.wantWarn {
				t.Errorf("matrixWarning(%q, %q) = %q (warn=%v); wantWarn=%v",
					tc.platform, tc.jdk, got, gotWarn, tc.wantWarn)
			}
			if tc.wantWarn {
				// Sanity: the message must surface both the offending
				// platform and the expected jdk so the operator can
				// course-correct.
				if !strings.Contains(got, tc.platform) {
					t.Errorf("warning %q should mention platform %q", got, tc.platform)
				}
				if !strings.Contains(got, "expected jdk=") {
					t.Errorf("warning %q should mention 'expected jdk='", got)
				}
			}
		})
	}
}
