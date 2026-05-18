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

	if err := defaultRunner.RunDockerBuild(ctx, r, "" /* outDir unused for image */, "" /* outTmp unused */); err != nil {
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

	// Gradle's docker plugin typically tags the image as
	// `<group>/<name>:<version>` (e.g. tronprotocol/java-tron:GreatVoyage-…).
	// We don't try to discover that tag — instead we explicitly re-tag
	// to whatever the user asked for in build.image_tag.
	//
	// To know what to re-tag, we look at the *last* image produced
	// during the build. `docker image ls -q --filter "label=trond.build=<key>"`
	// would be cleaner but requires the gradle plugin to set a label
	// (it doesn't by default). Simpler approach: gradle dockerBuild
	// already prints the image ID; we capture it via a `docker images`
	// query against the cache key's parent.
	//
	// For Phase 3 we use the simplest workable path: ask docker which
	// image was created most recently and tag that as our target.
	// This works for the single-build serialized lock case (FR-015
	// flock around the cache key); concurrent builds would clobber.
	imageID, err := mostRecentlyCreatedImage(ctx)
	if err != nil {
		return nil, output.NewErrorf("BUILD_FAILED", output.ExitGeneralError,
			"locate produced image: %s", err.Error())
	}
	if imageID == "" {
		return nil, output.NewError("BUILD_FAILED", output.ExitGeneralError,
			"gradle finished but no docker image appeared in `docker images`").
			WithSuggestions(
				"Verify the gradle task you ran actually produces a docker image",
				"Common task names: dockerBuild, jib, bootBuildImage",
			)
	}
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

// mostRecentlyCreatedImage returns the image ID at the top of
// `docker images -q` — the most recently created image on the
// host. Used to locate the artifact gradle's dockerBuild plugin
// just produced (no standard way to ask the plugin directly).
func mostRecentlyCreatedImage(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "images", "-q", "--no-trunc")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker images: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line, nil // first line = newest
	}
	return "", nil
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
func validateImageEntrypoint(ctx context.Context, tag string) error {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format={{.Config.Entrypoint}}|{{.Config.Cmd}}", tag)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker inspect %s: %w", tag, err)
	}
	got := strings.TrimSpace(string(out))
	// "[]|[]" is what docker prints for entirely missing entrypoint + cmd.
	if got == "[]|[]" || got == "|" {
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

// imageExistsLocally checks whether the named image ID is still
// present in the host's docker storage. A `trond build prune` run
// outside trond's purview, or `docker system prune`, can delete the
// image; the bookkeeping JSON then points at nothing.
func imageExistsLocally(ctx context.Context, imageID string) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect",
		"--format={{.Id}}", imageID)
	return cmd.Run() == nil
}
