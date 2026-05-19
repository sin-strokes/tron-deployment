package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tronprotocol/tron-deployment/internal/output"
)

// imageMetadata is the bookkeeping record written under
// ${cacheDir}/images/<cache-key>.json. Captures what we built
// locally so Lookup() can verify the image still exists in the
// host's docker storage and so prune (Phase 5) can `docker image
// rm <id>` to actually free the layers.
type imageMetadata struct {
	CacheKey string `json:"cache_key"`
	Tag      string `json:"tag"`
	ImageID  string `json:"image_id"`
}

// buildImage runs gradle to produce a docker image artifact, tags
// the result with the user-supplied build.image_tag, captures the
// image ID, and persists both the build manifest and the per-cache-
// key image bookkeeping JSON. Mirrors buildJAR's lifecycle:
//
//  1. (Optional) clean up stale state from a prior cancelled run.
//  2. Invoke gradle inside the builder container.
//  3. Re-tag the produced image as <build.image_tag>.
//  4. Resolve the image ID via `docker inspect` (so prune knows
//     which sha256 to delete later).
//  5. Validate the image has a runnable ENTRYPOINT (FR-011 for image
//     kind: produced image must be runnable as a java-tron node).
//  6. Persist Manifest + images/<key>.json.
//
// Cancellation: ctx cancellation kills the docker subprocess. We
// don't try to docker-image-rm a half-built image — gradle leaves
// its own partial state inside the container that's already torn
// down with --rm.
func buildImage(ctx context.Context, r *resolved, started time.Time) (*Manifest, error) {
	tag := r.req.ImageTag
	if tag == "" {
		return nil, output.NewError("VALIDATION_ERROR", output.ExitValidationError,
			"build.image_tag is required when artifact = image")
	}

	// The wrapper script (dockerBuildScript_Image) snapshots image
	// IDs before + after gradle into /out/new-image-ids, so we
	// need /out mounted. outDir is the host path that's bound.
	outDir := filepath.Join(CacheDir(), "out")

	if err := defaultRunner.RunDockerBuild(ctx, r, outDir, "" /* outTmp unused for image */); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, output.NewErrorf("BUILD_CANCELLED", 130,
				"build cancelled by user").
				WithSuggestions("Re-run when ready")
		}
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"gradle dockerBuild failed: %s", err.Error()).
			WithSuggestions(
				"Inspect the gradle output above for build errors",
				"Verify the source tree's build.gradle defines the dockerBuild task",
			)
	}

	// Read the wrapper's before/after diff. Each line is one
	// image ID that gradle's task produced this run — no race with
	// other host docker activity (gradle's `docker images` runs
	// inside our serialized build window).
	newImageIDs, err := readNewImageIDs(outDir)
	if err != nil {
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"locate produced image: %s", err.Error())
	}
	if len(newImageIDs) == 0 {
		return nil, output.NewError("BUILD_FAILED", output.ExitGeneralError,
			"gradle finished but no new docker image appeared on the host").
			WithSuggestions(
				"Verify the gradle task you ran actually produces a docker image",
				"Common task names: dockerBuild, jib, bootBuildImage",
			)
	}
	// Pick the LAST new image — gradle's docker plugin typically
	// produces intermediate layers + a final tagged image; the
	// final one is the deploy artifact. (If gradle produced
	// multiple final images, the user's gradle_task name selected
	// only one, so picking the most-recent is unambiguous.)
	imageID := newImageIDs[len(newImageIDs)-1]
	if err := dockerTag(ctx, imageID, tag); err != nil {
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"docker tag %s %s: %s", imageID, tag, err.Error())
	}

	if err := validateImageEntrypoint(ctx, tag); err != nil {
		return nil, output.NewErrorf("INVALID_ARTIFACT", output.ExitGeneralError,
			"produced image is not runnable: %s", err.Error()).
			WithSuggestions(
				"Verify the gradle docker task produces an image with a runnable ENTRYPOINT",
			)
	}

	if err := writeImageMetadata(r.cacheKeyStr, tag, imageID); err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"persist image metadata: %s", err.Error())
	}

	manifest := &Manifest{
		CacheKey:           r.cacheKeyStr,
		SourcePath:         r.src.Path,
		SourceRevision:     r.src.ResolvedRevision,
		PatchHash:          r.src.PatchHash,
		Dirty:              r.src.DirtyState,
		BuilderImage:       r.imageRef,
		BuilderImageDigest: r.imageDigest,
		JDKVersion:         r.req.JDKVersion,
		ArtifactKind:       "image",
		ImageTag:           tag,
		ImageID:            imageID,
		GradleTask:         r.req.GradleTask,
		GradleArgs:         r.req.GradleArgs,
		Builder:            r.req.Builder,
		Platform:           r.req.Platform,
		DurationMs:         time.Since(started).Milliseconds(),
		CreatedAt:          time.Now().UTC(),
	}
	if err := Save(manifest); err != nil {
		return nil, output.NewErrorf("INTERNAL_ERROR", output.ExitGeneralError,
			"persist manifest: %s", err.Error())
	}
	return manifest, nil
}

// readNewImageIDs returns the image IDs the wrapper script
// recorded as newly created during the gradle run.
//
// The wrapper computes (after \ before) of `docker images -q
// --no-trunc | sort -u` and writes the result to
// /out/new-image-ids. Because both snapshots happen inside the
// builder's serialized window (FR-015 flock), the diff is robust
// against the user's other host docker activity.
//
// Empty file = gradle ran but produced no image. Returns the IDs
// in the order docker reported them (typically by creation time
// thanks to sort order on hashes — not strictly, but stable enough
// for our needs since the build is gated to one image producer).
func readNewImageIDs(outDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(outDir, "new-image-ids"))
	if err != nil {
		return nil, fmt.Errorf("read new-image-ids: %w", err)
	}
	var ids []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ids = append(ids, line)
	}
	return ids, nil
}

func dockerTag(ctx context.Context, imageID, tag string) error {
	cmd := exec.CommandContext(ctx, "docker", "tag", imageID, tag)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// validateImageEntrypoint asserts the image has a non-empty
// ENTRYPOINT or CMD — FR-011's image-side analogue. Run via
// `docker inspect`; we don't try to actually `docker run` the image
// because that's expensive and we don't have arguments to test it
// with.
//
// Uses {{len ...}} to compare numbers (0/0 = both empty) rather
// than string-compare the formatted array — docker version drift
// changes the printed representation but never the array length.
func validateImageEntrypoint(ctx context.Context, tag string) error {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format={{len .Config.Entrypoint}}/{{len .Config.Cmd}}", tag)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker inspect %s: %w", tag, err)
	}
	got := strings.TrimSpace(string(out))
	if got == "0/0" {
		return fmt.Errorf("image %s has neither ENTRYPOINT nor CMD", tag)
	}
	return nil
}

func writeImageMetadata(cacheKey, tag, imageID string) error {
	if err := os.MkdirAll(filepath.Join(CacheDir(), "images"), 0o700); err != nil {
		return err
	}
	meta := imageMetadata{CacheKey: cacheKey, Tag: tag, ImageID: imageID}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(CacheDir(), "images", cacheKey+".json")
	return os.WriteFile(path, data, 0o600)
}

// readImageMetadata returns the bookkeeping record for a cache key,
// or os.ErrNotExist when no image was produced for that key. Used
// by cache.Lookup() to verify the local image still exists before
// declaring a cache hit.
func readImageMetadata(cacheKey string) (*imageMetadata, error) {
	path := filepath.Join(CacheDir(), "images", cacheKey+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta imageMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("decode image metadata: %w", err)
	}
	return &meta, nil
}

// imageTagExistsLocally checks whether the docker tag is still
// resolvable in the host's image store. We deliberately check tag
// (not image ID): a `docker rmi <tag>` may detach the tag while the
// underlying image stays alive via another tag, but compose's
// `image: <tag>` field is what trond renders — so tag presence is
// the authoritative signal. Tag-side check also catches the case
// where the user retagged trond's image to something else.
func imageTagExistsLocally(ctx context.Context, tag string) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect",
		"--format={{.Id}}", tag)
	return cmd.Run() == nil
}
