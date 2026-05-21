package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tronprotocol/tron-deployment/internal/build"
)

// seedBuildCache writes one JAR manifest + artifact under an isolated
// state dir so the build_* MCP tools have something to walk. Returns
// the cache key so callers can target it in tool args.
func seedBuildCache(t *testing.T, key string, createdAt time.Time, size int) {
	t.Helper()
	if err := build.EnsureCacheDirs(); err != nil {
		t.Fatalf("EnsureCacheDirs: %v", err)
	}
	artifactPath := filepath.Join(build.CacheDir(), "out", key+".jar")
	if err := os.WriteFile(artifactPath, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write jar: %v", err)
	}
	m := &build.Manifest{
		CacheKey:           key,
		SourcePath:         "/some/src",
		SourceRevision:     "abc1234567890abcdef1234567890abcdef12345",
		BuilderImage:       "eclipse-temurin:8-jdk-jammy",
		BuilderImageDigest: "sha256:aaaa",
		JDKVersion:         "8",
		ArtifactKind:       "jar",
		ArtifactPath:       artifactPath,
		GradleTask:         "shadowJar",
		Builder:            "docker",
		Platform:           "linux/amd64",
		CreatedAt:          createdAt,
	}
	if err := build.Save(m); err != nil {
		t.Fatalf("Save manifest: %v", err)
	}
}

// TestBuildList_RoundTrip: a seeded cache shows up through the MCP
// build_list tool. Mirrors the cmd/build_list smoke test but
// exercises the JSON-RPC tool path end to end.
//
// IMPORTANT: newConnectedPair sets paths.BaseDir to its own tempdir,
// so seeding MUST happen after that call so the seed lands where
// the in-process tool will look for it.
func TestBuildList_RoundTrip(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()
	seedBuildCache(t, "abc12345-bdeadbeef", time.Now(), 12345)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "build_list",
	})
	if err != nil {
		t.Fatalf("CallTool build_list: %v", err)
	}
	if res.IsError {
		t.Fatalf("build_list returned IsError: %s", extractText(t, res))
	}

	var body struct {
		Count   int           `json:"count"`
		Entries []*buildEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &body); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, extractText(t, res))
	}
	if body.Count != 1 || len(body.Entries) != 1 {
		t.Fatalf("expected exactly 1 entry; got count=%d len=%d", body.Count, len(body.Entries))
	}
	if body.Entries[0].CacheKey != "abc12345-bdeadbeef" {
		t.Errorf("CacheKey = %q; want abc12345-bdeadbeef", body.Entries[0].CacheKey)
	}
	if body.Entries[0].SizeBytes != 12345 {
		t.Errorf("SizeBytes = %d; want 12345", body.Entries[0].SizeBytes)
	}
}

// TestBuildInspect_NotFound: missing key surfaces the structured
// error envelope (IsError + NOT_FOUND code).
func TestBuildInspect_NotFound(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "build_inspect",
		Arguments: map[string]any{"cache_key": "does-not-exist"},
	})
	if err != nil {
		t.Fatalf("CallTool build_inspect: %v", err)
	}
	if !res.IsError {
		t.Fatal("build_inspect for missing key should set IsError=true")
	}
	body := extractText(t, res)
	var env map[string]any
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, body)
	}
	if env["error_code"] != "NOT_FOUND" {
		t.Errorf("error_code = %v; want NOT_FOUND", env["error_code"])
	}
}

// TestBuildPrune_EmptyPolicyRejected pins the validation guard the
// CLI also enforces — empty policy is virtually always an LLM
// mistake, not an intent to no-op. Better to surface a structured
// error than silently dry-run-with-no-plan.
func TestBuildPrune_EmptyPolicyRejected(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "build_prune",
	})
	if err != nil {
		t.Fatalf("CallTool build_prune: %v", err)
	}
	if !res.IsError {
		t.Fatal("empty-policy build_prune should set IsError=true")
	}
	body := extractText(t, res)
	if !contains(body, "needs at least one of") {
		t.Errorf("error message should explain why; got %q", body)
	}
}

// TestBuildPrune_DryRunPlan: a seeded cache + matching policy
// produces a plan; the seeded JAR is still on disk afterward.
func TestBuildPrune_DryRunPlan(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()
	seedBuildCache(t, "doomed", time.Now().Add(-10*24*time.Hour), 100)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "build_prune",
		Arguments: map[string]any{
			"older_than": "24h",
			// confirm omitted ⇒ DryRun=true
		},
	})
	if err != nil {
		t.Fatalf("CallTool build_prune: %v", err)
	}
	if res.IsError {
		t.Fatalf("build_prune returned IsError: %s", extractText(t, res))
	}
	var pr struct {
		DryRun     bool          `json:"dry_run"`
		FreedBytes int64         `json:"freed_bytes"`
		Plan       []*buildEntry `json:"plan"`
		Removed    []*buildEntry `json:"removed"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &pr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !pr.DryRun {
		t.Error("dry_run should be true when confirm omitted")
	}
	if len(pr.Plan) != 1 || pr.Plan[0].CacheKey != "doomed" {
		t.Errorf("plan should contain 'doomed'; got %d entries", len(pr.Plan))
	}
	if len(pr.Removed) != 0 {
		t.Errorf("DryRun should not have removed anything; got %d removed", len(pr.Removed))
	}
	if pr.FreedBytes != 100 {
		t.Errorf("FreedBytes = %d; want 100 (still reported on dry-run)", pr.FreedBytes)
	}
}

// TestBuildPrune_KeepLastAloneConfirmRejected is the review-pass-4
// footgun guard: `keep_last=N confirm=true` with no other filter
// would wipe everything except the N newest entries — too easy for
// an LLM to invoke under a prompt like "trim cache to recent
// entries". The guard requires either all=true (explicit) or a
// scoping filter (orphan_only / older_than) before this near-wipe.
//
// Dry-run (confirm omitted) MUST still be allowed: the plan output
// is the obvious affordance for an operator surveying what
// keep_last would remove.
func TestBuildPrune_KeepLastAloneConfirmRejected(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	t.Run("rejects keep_last+confirm with no other filter", func(t *testing.T) {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "build_prune",
			Arguments: map[string]any{
				"keep_last": 1,
				"confirm":   true,
			},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Fatal("keep_last+confirm without scope should set IsError=true")
		}
		body := extractText(t, res)
		if !contains(body, "would wipe everything except") {
			t.Errorf("error message should explain the footgun; got %q", body)
		}
	})

	t.Run("allows keep_last dry-run (no confirm)", func(t *testing.T) {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "build_prune",
			Arguments: map[string]any{"keep_last": 1},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Errorf("keep_last dry-run should succeed; got error %s", extractText(t, res))
		}
	})

	t.Run("allows keep_last + all + confirm (explicit ack)", func(t *testing.T) {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "build_prune",
			Arguments: map[string]any{
				"keep_last": 1,
				"all":       true,
				"confirm":   true,
			},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Errorf("keep_last+all+confirm should succeed (explicit acknowledge); got %s", extractText(t, res))
		}
	})
}

// TestBuildPrune_ZeroDurationDoesNotBypassFootgun is the review-
// pass-5 hardening for the MCP footgun guard. The previous check
// used `args.OlderThan == ""` to detect "no scoping filter", but
// `older_than: "0s"` (or "0h", or "-1h") parses fine, ends up as
// duration zero / negative, then `selectForPrune`'s `> 0` gate
// silently ignores it — bypassing the guard the LLM cannot have
// realized existed. Fix parses older_than FIRST and treats only
// positive durations as a real scoping filter.
func TestBuildPrune_ZeroDurationDoesNotBypassFootgun(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	cases := []struct {
		name string
		dur  string
	}{
		{"zero seconds", "0s"},
		{"zero hours", "0h"},
		{"negative", "-1h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "build_prune",
				Arguments: map[string]any{
					"keep_last":  1,
					"confirm":    true,
					"older_than": c.dur,
				},
			})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Errorf("older_than=%q must NOT bypass the footgun guard; "+
					"got IsError=false (would have wiped everything-but-newest)", c.dur)
			}
			body := extractText(t, res)
			if !contains(body, "would wipe everything except") {
				t.Errorf("error should mention the footgun; got %q", body)
			}
		})
	}
}

// TestBuildPrune_BadDuration: invalid older_than surfaces a clear
// validation error instead of silently being ignored.
func TestBuildPrune_BadDuration(t *testing.T) {
	session, cleanup := newConnectedPair(t)
	defer cleanup()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "build_prune",
		Arguments: map[string]any{
			"older_than": "7days", // not a valid Go duration
		},
	})
	if err != nil {
		t.Fatalf("CallTool build_prune: %v", err)
	}
	if !res.IsError {
		t.Fatal("bad duration should set IsError=true")
	}
	body := extractText(t, res)
	if !contains(body, "VALIDATION_ERROR") {
		t.Errorf("error envelope should carry VALIDATION_ERROR; got %q", body)
	}
}

// buildEntry mirrors the subset of build.Entry fields these tests
// assert on. Tests must not depend on the full struct shape so a
// future additive field doesn't force every test to update.
type buildEntry struct {
	CacheKey  string `json:"cache_key"`
	SizeBytes int64  `json:"size_bytes"`
	Orphaned  bool   `json:"orphaned"`
}

// contains is a tiny dependency-free substring check; the package
// already has one in build_ssh_test.go (apply) but not in mcp/. We
// keep this local to avoid coupling test packages.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
