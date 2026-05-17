package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source describes a java-tron checkout to build.
//
// The caller passes Path + RevisionSpec; trond fills in
// ResolvedRevision, DirtyState, PatchHash by shelling out to git.
type Source struct {
	Path             string // canonicalized abs path (FR-021)
	RevisionSpec     string // "HEAD" | branch | tag | sha
	ResolvedRevision string // full sha after git rev-parse
	DirtyState       bool
	PatchHash        string // sha256 prefix; FR-002 (combines diff + status)
}

// Resolve fills ResolvedRevision, DirtyState, and PatchHash by
// running git commands inside s.Path. trond shells out to /usr/bin/git
// rather than depending on go-git so the binary stays small and
// behaviour matches the user's installed git exactly.
func (s *Source) Resolve(ctx context.Context) error {
	abs, err := filepath.Abs(s.Path)
	if err != nil {
		return fmt.Errorf("canonicalize source path: %w", err)
	}
	s.Path = abs

	rev, err := s.runGit(ctx, "rev-parse", s.RevisionSpec)
	if err != nil {
		return fmt.Errorf("resolve revision %q: %w", s.RevisionSpec, err)
	}
	s.ResolvedRevision = strings.TrimSpace(rev)

	// "HEAD" is the only spec where local dirty state is meaningful —
	// for an explicit branch/tag/sha the user asked for *that* tree,
	// dirty local edits don't change which artifact they want.
	if s.RevisionSpec == "HEAD" {
		dirty, patch, err := s.computeDirty(ctx)
		if err != nil {
			return fmt.Errorf("dirty detection: %w", err)
		}
		s.DirtyState = dirty
		s.PatchHash = patch
	}
	return nil
}

// computeDirty returns (true, patchHash) if the working tree differs
// from HEAD in any way trond cares about. Per FR-002 the patch hash
// MUST include untracked files (regression bug found in pass 1 of the
// design review) — `git diff` alone misses brand-new files. We hash
// the concatenation of:
//
//	git diff HEAD                          (tracked + staged + unstaged)
//	git status --porcelain -uall           (untracked files + modes)
func (s *Source) computeDirty(ctx context.Context) (bool, string, error) {
	diff, err := s.runGit(ctx, "diff", "HEAD")
	if err != nil {
		return false, "", fmt.Errorf("git diff HEAD: %w", err)
	}
	status, err := s.runGit(ctx, "status", "--porcelain", "-uall")
	if err != nil {
		return false, "", fmt.Errorf("git status --porcelain -uall: %w", err)
	}
	if diff == "" && status == "" {
		return false, "", nil
	}
	h := sha256.New()
	h.Write([]byte(diff))
	// Use NUL as a separator the user can't legitimately produce in
	// either stream — defends against the (unlikely) case where the
	// two streams' concatenation happens to collide with another
	// combination.
	h.Write([]byte{0})
	h.Write([]byte(status))
	return true, hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Source) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", s.Path}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		// Surface the stderr tail so the user sees git's complaint.
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s",
				strings.Join(args, " "),
				strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}
