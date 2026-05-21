#!/usr/bin/env bash
# Re-resolves Eclipse Temurin builder image tags to their current
# sha256 digests and rewrites internal/build/pins/builder_image_digests.json.
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
# Requirements: docker, jq (apt-get install jq | brew install jq).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIN_FILE="$REPO_ROOT/internal/build/pins/builder_image_digests.json"

# Map of JDK version → upstream tag. Keep sorted by version.
declare -a JDK_VERSIONS=(8 11 17 21)
declare -A TAG_FOR=(
  [8]="eclipse-temurin:8-jdk-jammy"
  [11]="eclipse-temurin:11-jdk-jammy"
  [17]="eclipse-temurin:17-jdk-jammy"
  [21]="eclipse-temurin:21-jdk-jammy"
)

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required (used to pull + inspect images)" >&2
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
  echo "[refresh-builder-pins] resolving $tag (JDK $jdk)..." >&2
  docker pull --quiet "$tag" >/dev/null

  # `docker inspect` returns the canonical RepoDigest for the local image.
  # Strip the `<repo>@` prefix so we end up with just `sha256:<hex>`.
  digest="$(docker inspect --format='{{ index .RepoDigests 0 }}' "$tag" | sed 's/.*@//')"

  if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "error: unexpected digest format for $tag: $digest" >&2
    exit 1
  fi

  echo "[refresh-builder-pins]   $jdk → $tag @ $digest" >&2

  new_pins="$(jq --arg jdk "$jdk" --arg ref "$tag" --arg digest "$digest" \
    '. + { ($jdk): { ref: $ref, digest: $digest } }' <<<"$new_pins")"
done

new_doc="$(jq --argjson pins "$new_pins" \
  '{
    "$comment": "Pinned digests for builder images. Bumped per trond release via `make refresh-builder-pins`. Each entry resolves jdk_version -> reference (canonical name@sha256:...). Cache key (FR-002) incorporates the digest so pin bumps invalidate stale artifacts.",
    "schema_version": "1.0.0",
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
