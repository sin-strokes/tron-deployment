package cmd

import (
	"testing"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/build"
)

// Cobra-layer tests for `trond build list / inspect / prune`. The
// real cache walking is exercised in internal/build/cache_ops_test.go;
// these tests pin the CLI-shaped behaviors that live in cmd/build_*.go:
// filter / sort helpers, the prune validation guard, the humanBytes
// formatter the output table relies on.

func TestFilterEntriesByKind(t *testing.T) {
	entries := []*build.Entry{
		{Manifest: &build.Manifest{CacheKey: "j1", ArtifactKind: "jar"}},
		{Manifest: &build.Manifest{CacheKey: "i1", ArtifactKind: "image"}},
		{Manifest: &build.Manifest{CacheKey: "j2", ArtifactKind: "jar"}},
	}
	t.Run("all is no-op", func(t *testing.T) {
		got := filterEntriesByKind(entries, "all")
		if len(got) != 3 {
			t.Errorf("'all' should return everything; got %d/3", len(got))
		}
	})
	t.Run("jar filter", func(t *testing.T) {
		got := filterEntriesByKind(entries, "jar")
		if len(got) != 2 {
			t.Errorf("jar filter should return 2; got %d", len(got))
		}
	})
	t.Run("image filter", func(t *testing.T) {
		got := filterEntriesByKind(entries, "image")
		if len(got) != 1 || got[0].CacheKey != "i1" {
			t.Errorf("image filter should return [i1]; got %d entries", len(got))
		}
	})
}

func TestSortEntries(t *testing.T) {
	now := time.Now()
	mkEntries := func() []*build.Entry {
		return []*build.Entry{
			{Manifest: &build.Manifest{CacheKey: "small-new", CreatedAt: now}, SizeBytes: 100},
			{Manifest: &build.Manifest{CacheKey: "big-old", CreatedAt: now.Add(-2 * time.Hour)}, SizeBytes: 10_000},
			{Manifest: &build.Manifest{CacheKey: "mid-mid", CreatedAt: now.Add(-1 * time.Hour)}, SizeBytes: 1_000},
		}
	}

	t.Run("oldest", func(t *testing.T) {
		es := mkEntries()
		if err := sortEntries(es, "oldest"); err != nil {
			t.Fatal(err)
		}
		want := []string{"big-old", "mid-mid", "small-new"}
		for i, e := range es {
			if e.CacheKey != want[i] {
				t.Errorf("oldest[%d] = %q; want %q", i, e.CacheKey, want[i])
			}
		}
	})

	t.Run("size", func(t *testing.T) {
		es := mkEntries()
		if err := sortEntries(es, "size"); err != nil {
			t.Fatal(err)
		}
		want := []string{"big-old", "mid-mid", "small-new"}
		for i, e := range es {
			if e.CacheKey != want[i] {
				t.Errorf("size[%d] = %q; want %q", i, e.CacheKey, want[i])
			}
		}
	})

	t.Run("invalid order returns error", func(t *testing.T) {
		if err := sortEntries(mkEntries(), "alphabetical"); err == nil {
			t.Error("expected error for unknown sort order")
		}
	})
}

// TestHumanBytes pins the table formatter. The numbers feed both
// 'trond build list' (table mode) and 'trond build prune' (dry-run
// output), so a regression here is loudly visible.
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "-"},
		{500, "500B"},
		{2 * 1024, "2KB"},
		{5 * 1024 * 1024, "5MB"},
		{int64(1.5 * 1024 * 1024 * 1024), "1.5GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q; want %q", c.n, got, c.want)
		}
	}
}
