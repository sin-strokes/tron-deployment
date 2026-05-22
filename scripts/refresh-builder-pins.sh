#!/usr/bin/env bash
# Re-resolves Eclipse Temurin builder image tags to PER-PLATFORM
# sha256 digests and rewrites internal/build/pins/builder_image_digests.json.
#
# Why per-platform (not the manifest-list digest):
#   docker run --platform <arch> image@<manifest-list-digest>
# fails with "cannot overwrite digest" because the per-arch image
# trond actually runs has a DIFFERENT digest from the manifest list.
# So we pin the per-arch digest directly via `docker manifest inspect`.
#
# Per spec/002 FR-012: the embedded pin file is the source of truth
# for trond's reproducible builder images. Bumping the pins is a
# release-prep step — `make refresh-builder-pins` calls this script,
# review the diff, commit with the rest of the release tag.
#
# Usage:
#   ./scripts/refresh-builder-pins.sh           # rewrite the JSON in place
#   ./scripts/refresh-builder-pins.sh --dry-run # print without writing
#
# Requirements: docker, jq, docker manifest API (default-on in
# modern docker; `docker manifest inspect` works against the
# configured registry).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIN_FILE="$REPO_ROOT/internal/build/pins/builder_image_digests.json"

declare -a JDK_VERSIONS=(8 11 17 21)
declare -A TAG_FOR=(
  [8]="eclipse-temurin:8-jdk-jammy"
  [11]="eclipse-temurin:11-jdk-jammy"
  [17]="eclipse-temurin:17-jdk-jammy"
  [21]="eclipse-temurin:21-jdk-jammy"
)
declare -a PLATFORMS=("linux/amd64" "linux/arm64")

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required" >&2
  exit 64
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is required for safe JSON rewriting" >&2
  exit 64
fi

# Build the new JSON in-memory, then atomically swap.
new_pins='{}'
for jdk in "${JDK_VERSIONS[@]}"; do
  tag="${TAG_FOR[$jdk]}"
  echo "[refresh-builder-pins] inspecting manifest list for $tag (JDK $jdk)..." >&2

  # `docker manifest inspect` queries the registry without pulling.
  # The output describes a multi-arch manifest list with one entry
  # per platform; we pluck the per-arch digest with jq.
  manifest_json="$(docker manifest inspect "$tag")"

  platforms_obj='{}'
  for platform in "${PLATFORMS[@]}"; do
    arch="${platform#linux/}"  # linux/amd64 -> amd64
    digest="$(jq -r --arg arch "$arch" '
      .manifests[]
      | select(.platform.os == "linux" and .platform.architecture == $arch)
      | .digest
    ' <<<"$manifest_json")"

    if [[ -z "$digest" || "$digest" == "null" ]]; then
      echo "error: no $platform variant in manifest list for $tag" >&2
      exit 1
    fi
    if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
      echo "error: unexpected digest format for $tag/$platform: $digest" >&2
      exit 1
    fi

    echo "[refresh-builder-pins]   $jdk/$platform → $digest" >&2
    platforms_obj="$(jq --arg p "$platform" --arg d "$digest" \
      '. + { ($p): $d }' <<<"$platforms_obj")"
  done

  new_pins="$(jq --arg jdk "$jdk" --arg ref "$tag" --argjson platforms "$platforms_obj" \
    '. + { ($jdk): { ref: $ref, platforms: $platforms } }' <<<"$new_pins")"
done

new_doc="$(jq --argjson pins "$new_pins" \
  '{
    "$comment": "Pinned per-platform digests for builder images. Bumped per trond release via `make refresh-builder-pins`. Each entry resolves (jdk_version, platform) → per-arch image digest (NOT the multi-arch manifest list digest — pinning that and combining with `docker run --platform X` fails with `cannot overwrite digest`). Cache key (FR-002) incorporates the digest so pin bumps invalidate stale artifacts.",
    "schema_version": "2.0.0",
    "pins": $pins
  }' <<<'{}')"

if [[ $DRY_RUN -eq 1 ]]; then
  echo "$new_doc"
  exit 0
fi

# Atomic write.
tmp="$(mktemp "${PIN_FILE}.XXXXXX")"
printf '%s\n' "$new_doc" > "$tmp"
mv "$tmp" "$PIN_FILE"

echo "[refresh-builder-pins] wrote $PIN_FILE"
echo "[refresh-builder-pins] review the diff and commit alongside the trond version bump."
