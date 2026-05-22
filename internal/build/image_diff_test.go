package build

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestComputeNewImages covers the host-side diff that replaced the
// in-container `comm -13` step (review pass 2 fix D). It's the
// single piece of logic between "wrapper script wrote two
// snapshots" and "trond picks the produced image tag" — every
// downstream behaviour in buildImage depends on it being right.
func TestComputeNewImages(t *testing.T) {
	cases := []struct {
		name   string
		before []string
		after  []string
		want   []string
	}{
		{
			name:   "empty before, one new",
			before: []string{},
			after:  []string{"sha256:abc"},
			want:   []string{"sha256:abc"},
		},
		{
			name:   "no change",
			before: []string{"sha256:abc"},
			after:  []string{"sha256:abc"},
			want:   nil,
		},
		{
			name:   "one added, one preserved",
			before: []string{"sha256:abc"},
			after:  []string{"sha256:abc", "sha256:def"},
			want:   []string{"sha256:def"},
		},
		{
			name:   "preserves after-order, drops dupes",
			before: []string{"sha256:abc", "sha256:xyz"},
			after:  []string{"sha256:abc", "sha256:def", "sha256:ghi", "sha256:xyz"},
			want:   []string{"sha256:def", "sha256:ghi"},
		},
		{
			name:   "image removed AND added — diff only reports new",
			before: []string{"sha256:abc", "sha256:def"},
			after:  []string{"sha256:abc", "sha256:ghi"},
			want:   []string{"sha256:ghi"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeNewImages(tc.before, tc.after)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("computeNewImages(%v, %v) = %v; want %v",
					tc.before, tc.after, got, tc.want)
			}
		})
	}
}

// TestReadNewImageIDs_PerCacheKeyIsolation exercises the
// regression-A fix: concurrent image builds with different cache
// keys must not see each other's snapshot files. We simulate by
// planting two distinct (before, after) pairs under different
// per-cache-key filenames and asserting readNewImageIDs returns the
// per-key diff.
func TestReadNewImageIDs_PerCacheKeyIsolation(t *testing.T) {
	outDir := t.TempDir()

	keyA := "abc12345-deadbe"
	keyB := "789xyzab-cafebe"

	// Build A: empty before, one new id.
	if err := os.WriteFile(filepath.Join(outDir, keyA+"-images-before"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, keyA+"-images-after"), []byte("sha256:aaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Build B: completely different before/after.
	if err := os.WriteFile(filepath.Join(outDir, keyB+"-images-before"), []byte("sha256:bbb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, keyB+"-images-after"), []byte("sha256:bbb\nsha256:ccc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gotA, err := readNewImageIDs(outDir, keyA)
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if !reflect.DeepEqual(gotA, []string{"sha256:aaa"}) {
		t.Errorf("A: got %v; want [sha256:aaa]", gotA)
	}

	// After reading, A's snapshot files should be removed (cleanup).
	if _, err := os.Stat(filepath.Join(outDir, keyA+"-images-before")); !os.IsNotExist(err) {
		t.Errorf("A's before file should be cleaned up; stat err=%v", err)
	}

	// B's files are untouched — A's read mustn't have stomped them.
	if _, err := os.Stat(filepath.Join(outDir, keyB+"-images-before")); err != nil {
		t.Errorf("B's before file should still exist; stat err=%v", err)
	}

	gotB, err := readNewImageIDs(outDir, keyB)
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if !reflect.DeepEqual(gotB, []string{"sha256:ccc"}) {
		t.Errorf("B: got %v; want [sha256:ccc]", gotB)
	}
}

// TestDockerBuildScript_Image_PinsSafety enforces the FR-022
// argv-only contract for the image-build wrapper: no trond-side
// field names interpolated into the script body. Mirrors the JAR
// variant's TestDockerBuildScript_NoUserInputInterpolation.
func TestDockerBuildScript_Image_PinsSafety(t *testing.T) {
	forbidden := []string{
		"$GRADLE_TASK", "${GRADLE_TASK",
		"$GRADLE_ARGS", "${GRADLE_ARGS",
		"$IMAGE_TAG", "${IMAGE_TAG",
		"eval ", "exec ",
	}
	for _, f := range forbidden {
		if strings.Contains(dockerBuildScript_Image, f) {
			t.Errorf("dockerBuildScript_Image contains forbidden pattern %q", f)
		}
	}
	// Required forms: $@ for gradle args, $CACHE_KEY for per-build
	// snapshot path isolation, --filter dangling=false for the
	// multi-stage-intermediate-layer exclusion fix.
	for _, required := range []string{
		`"$@"`,
		"$CACHE_KEY",
		"--filter dangling=false",
	} {
		if !strings.Contains(dockerBuildScript_Image, required) {
			t.Errorf("dockerBuildScript_Image MUST contain %q", required)
		}
	}
}
