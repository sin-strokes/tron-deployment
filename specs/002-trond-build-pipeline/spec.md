# Feature Specification: trond Build Pipeline

**Feature Branch**: `feat/build-pipeline`
**Created**: 2026-05-08
**Last revised**: 2026-05-08 (self-review pass — see CHANGELOG below)
**Status**: Draft
**Input**: User description: "Add a `trond build` capability that produces deployable
java-tron artifacts (JAR or Docker image) from a source tree, integrated with
`trond apply` so a developer can iterate on java-tron code and redeploy with a
single command."

## Background

trond today consumes pre-built java-tron artifacts: an official Docker image
(`tronprotocol/java-tron:<tag>`) or a pre-built JAR. The "edit java-tron code,
test the change on a node" loop is unsupported — operators must context-switch
to tron-docker / java-tron's Gradle toolchain to produce a custom artifact,
then hand-stitch the result back into a trond intent.

This feature closes that loop. trond gains a `build` verb that orchestrates a
containerized Gradle invocation and an `apply`-side hook that resolves a
`build:` block in intent.yaml automatically.

## Non-Goals

trond will NOT implement Java compilation, dependency resolution, or the Gradle
DSL itself. The Java toolchain runs unchanged inside a container that trond
manages; trond is an orchestrator, not a re-implementation.

trond will NOT replace tron-docker's release-grade image build (signed,
SBOM-attached, multi-arch matrix). That stays in tron-docker's CI. trond's
`build` targets the development inner loop.

## Clarifications

### Session 2026-05-08

- Q: Should the builder image be reproducible or follow upstream? → A: Pin a
  specific sha256 digest per JDK version. Bump the pin in a trond release.
- Q: Keep a `--builder host` escape hatch for developers with local gradle? →
  A: Yes — same flags, skips the container path. Low maintenance cost.
- Q: How should SSH targets handle the build step? → A: Build locally, scp the
  resulting JAR to the remote target, start the node there. Don't ship source.
- Q: Is `trond build` a standalone command or only an apply-side phase? →
  A: Both. Standalone for CI / debugging; apply-side for the integrated loop.

### Session 2026-05-08 (self-review pass)

- Q: Should `build.rebuild` field exist (`always | on_change | never`)? →
  A: **Removed.** Semantics overlapped confusingly with revision = HEAD +
  dirty detection. The cache-key derivation already handles the legitimate
  cases; an explicit field added zero value.
- Q: Patch hash for dirty trees: just `git diff`? → A: **No.** `git diff`
  misses untracked files, which would silently cache-hit a stale artifact.
  Patch hash MUST combine `git diff` and `git status --porcelain -uall`
  (so a brand-new `.java` file invalidates the cache).
- Q: Configurable gradle task name? → A: Yes, as `build.gradle_task` field
  with sensible defaults (`shadowJar` for jar, `dockerBuild` for image).

## User Scenarios & Testing

### User Story 1 — Build a JAR from local source (Priority: P1)

A java-tron developer has cloned the source, modified a few files, and wants a
fat JAR they can hand to `trond apply` or run separately. They invoke
`trond build` against the source directory and receive a JSON response
containing the artifact path, source revision, and content hash.

**Why this priority**: This is the foundation. Until a single artifact can be
produced repeatably from a source tree, nothing else in this feature works.

**Independent Test**:
```bash
trond build --source ./java-tron --artifact jar -o json
```
Delivers a JAR file under `~/.trond/builds/out/` and a JSON manifest under
`~/.trond/builds/manifest/`.

**Acceptance Scenarios**:

1. **Given** a clean java-tron working tree at revision `abc123`, **When** the
   developer runs `trond build --source ./java-tron --artifact jar -o json`,
   **Then** trond pulls the pinned `eclipse-temurin:8-jdk` image (first run
   only), runs `./gradlew shadowJar` inside it, and emits
   `{"source_revision":"abc123", "artifact_path":"~/.trond/builds/out/abc123.jar",
   "sha256":"...", "duration_ms":..., "cache_hit": false}`.

2. **Given** the same revision built once already, **When** the developer
   re-runs the same command, **Then** trond returns the cached manifest with
   `cache_hit: true` and `duration_ms < 200`. The on-disk JAR is byte-identical.

3. **Given** uncommitted edits in the working tree **and** an untracked new
   file, **When** the developer runs the same command, **Then** trond
   computes a patch hash that combines `git diff` and `git status --porcelain
   -uall`, names the artifact `abc123+dirty-<patchsha>.jar`, and treats it as
   a distinct cache entry. Adding an untracked file MUST change the patch
   hash (regression guard against the v1 design bug found in self-review).

4. **Given** the build fails (compile error in user's code), **When** the
   command runs, **Then** trond surfaces a structured error envelope
   (`error_code: "BUILD_FAILED"`, message containing gradle's tail output,
   `suggestions` pointing at common causes) and exits 1.

5. **Given** Docker is not running on the host, **When** the developer
   invokes `trond build` without `--builder host`, **Then** trond exits with
   `error_code: "TARGET_UNREACHABLE"`, exit code 3, and a suggestion to start
   Docker or pass `--builder host`.

6. **Given** the user sends SIGINT during a running build, **When** trond
   receives the signal, **Then** trond kills the build container, removes
   any partial output from `out/`, leaves no manifest entry for the aborted
   build, and exits with `error_code: "BUILD_CANCELLED"`, exit code 130
   (standard for SIGINT).

7. **Given** two concurrent `trond build` invocations against the same
   source + revision, **When** they race, **Then** the second caller
   acquires a per-key file lock, waits for the first to complete, and
   returns a cache hit (no duplicate build).

### User Story 2 — Build and deploy in one command (Priority: P1)

A developer has an intent file that references a `build:` block instead of a
prebuilt image. They run `trond apply --intent dev.yaml`. trond resolves the
build first, then deploys a node against the freshly produced artifact.

**Why this priority**: This is the dev loop trond is being extended for. Story
1 alone leaves the build/deploy boundary manual.

**Independent Test**:
```bash
trond apply --intent examples/dev-local.yaml --auto-approve --wait -o json
```
Delivers a running node whose runtime points at the just-built artifact.

**Acceptance Scenarios**:

1. **Given** an intent with `build.source: ./java-tron, build.revision: HEAD,
   build.artifact: jar`, **When** `apply` runs and the revision has not been
   built before, **Then** trond invokes the build pipeline, then proceeds to
   `apply`. The JSON output contains both a `build` block and the usual `apply`
   fields.

2. **Given** the intent points at a build that's already cached, **When**
   `apply` runs and the node is already deployed at that same revision,
   **Then** the result is `no_change` and total duration is < 5 seconds.

3. **Given** the build succeeds but the resulting JAR is not a valid
   java-tron fat JAR (no `org.tron.program.FullNode` main class), **When**
   `apply` reaches the runtime step, **Then** trond fails with `error_code:
   "INVALID_ARTIFACT"`, NOT with a runtime crash inside the container.

4. **Given** `runtime: docker` with `artifact: image`, **When** trond renders
   the compose file, **Then** the service has `pull_policy: never` set, so
   compose does not attempt to pull the locally-built tag from a remote
   registry.

### User Story 3 — Build a Docker image, not a JAR (Priority: P2)

The same developer wants a runnable Docker image rather than a JAR (so the
node uses the docker runtime). They set `build.artifact: image` and a
`build.image_tag` in their intent.

**Independent Test**:
```bash
trond build --source ./java-tron --artifact image --tag trond-dev:abc123 -o json
```

**Acceptance Scenarios**:

1. **Given** a tron-docker-shaped source tree (gradle target `dockerBuild`),
   **When** the build runs, **Then** trond invokes the gradle docker plugin
   and produces a local image tagged `trond-dev:abc123`. The manifest records
   the image ID (sha256 digest).

2. **Given** the artifact is `image`, **When** `apply` runs, **Then** the
   rendered docker-compose references the local image tag directly with
   `pull_policy: never`.

### User Story 4 — Deploy over SSH using a locally built JAR (Priority: P2)

A developer builds on their laptop and deploys to a remote Linux VM via SSH.

**Independent Test**:
```bash
trond apply --intent examples/dev-ssh.yaml -o json
```
where the intent has both a `build:` block AND `target.type: ssh`.

**Acceptance Scenarios**:

1. **Given** a successful local build of `<sha>.jar`, **When** `apply` runs
   against an SSH target, **Then** trond scp's the JAR to the remote host's
   deployment directory, configures the systemd unit to point at it, and
   starts the service. No source is shipped to the remote.

2. **Given** the remote target already has the same `<sha>.jar` on disk (from
   a prior run), **When** `apply` runs, **Then** trond skips the transfer
   step and goes directly to the start phase.

3. **Given** a slow link and a large fat JAR (~200 MB), **When** the
   transfer starts, **Then** trond emits MCP progress notifications (same
   mechanism `snapshot download` uses) so a connected agent surfaces a
   live progress bar to the user.

4. **Given** the SSH target host lacks `scp` (some hardened distros), **When**
   preflight runs, **Then** trond reports a clear `error_code:
   "TARGET_MISSING_TOOL"` with a suggestion to install openssh-clients on
   the remote.

### User Story 5 — Use host gradle (no container) (Priority: P3)

A developer with a configured local Gradle daemon wants to skip the container
for max-iteration speed.

**Acceptance Scenarios**:

1. **Given** `--builder host` is passed (CLI) or `build.builder: host` is set
   (intent), **When** the build runs, **Then** trond invokes `./gradlew` from
   the source directory directly. JDK + Gradle version mismatches surface as
   `BUILD_FAILED` with `suggestions` pointing at the required versions.

### Edge Cases

- **Source tree on a separate filesystem with weird permissions**: trond
  mounts read-only so the build can't corrupt source. Gradle wrapper attempts
  to write to `~/.gradle/caches` — trond redirects this via mount.
- **Out-of-disk during a build**: detected by gradle's own error; trond
  surfaces with a disk-space suggestion.
- **Concurrent `trond build` calls on the same revision**: per-key
  `flock`-based serialization (FR-015); second caller waits then returns cache
  hit.
- **Builder image not present and offline**: surface clear network-error
  message, suggest `--builder host` fallback.
- **Revision is `HEAD` but git working tree is not a git repo**: error
  `error_code: "INVALID_SOURCE"` with suggestion to either commit or pass an
  explicit `--source-id <name>`.
- **Source path is a symlink**: resolve it before mounting; Docker's mount
  resolves symlinks at mount-time, so trond canonicalizes the path first.
- **Untracked file added between builds**: the patch hash MUST include
  untracked files (FR-002 explicit), preventing stale cache hits.

## Requirements

### Functional Requirements

- **FR-001**: trond MUST expose a `trond build` cobra command accepting at
  minimum `--source <path>`, `--artifact <jar|image>`, `--revision <rev>`,
  `--jdk <version>`, `--builder <docker|host>`, `--tag <tag>` (for image),
  `--gradle-task <name>` (override default).
- **FR-002**: The build cache MUST be content-addressed. The cache key for
  clean working trees is the resolved git sha. For dirty working trees, the
  key MUST incorporate a patch hash computed over the combined output of
  `git diff` AND `git status --porcelain -uall` (so untracked files
  invalidate cache).
- **FR-003**: Build outputs MUST live under `${TROND_STATE_DIR}/builds/`
  (default `~/.trond/builds/`) and respect `--state-dir` / `TROND_STATE_DIR`.
- **FR-004**: Each completed build MUST produce a JSON manifest matching
  `schemas/output/build.schema.json`.
- **FR-005**: The intent.yaml schema MUST accept a `build:` block that is
  mutually exclusive with `image:` and references source + revision. When
  `runtime: docker` + `artifact: image`, the rendered compose service MUST
  carry `pull_policy: never`.
- **FR-006**: `trond apply` MUST resolve a `build:` block by invoking the
  build pipeline before the render/deploy phases.
- **FR-007**: A build failure MUST set exit code 1 and `error_code:
  "BUILD_FAILED"`, with the gradle stderr tail (last ~50 lines) in the
  message. Output of a successful build MUST be silent on stdout in `-o
  json` mode; verbose mode streams gradle output to stderr.
- **FR-008**: The default builder MUST be `docker`. The host builder MUST be
  available as a flag override.
- **FR-009**: When the target is SSH, trond MUST build locally and transfer
  the JAR via scp. Source code MUST NOT be shipped to the remote. Transfers
  > 50 MB MUST emit MCP progress notifications.
- **FR-010**: Builds MUST be discoverable: `trond build list`, `trond build
  prune --keep N`, `trond build inspect <sha>`.
- **FR-011**: trond MUST validate the produced artifact is structurally valid
  (JAR contains `org.tron.program.FullNode` main class, or image has runnable
  ENTRYPOINT) before declaring success.
- **FR-012**: The pinned builder image MUST be reproducible: trond ships a
  `builder_image_digests.json` mapping `jdk_version → sha256:...`. Upgrading
  the pin is a trond release change, not a runtime change. A `make
  refresh-builder-pins` target MUST regenerate the file from current Temurin
  tags so digest drift is a one-command bump.
- **FR-013**: All build-related operations MUST be exposed through MCP as
  tools so AI agents can drive the dev loop. Tool annotations:
  - `build`: `idempotentHint=true`, `destructiveHint=false`
  - `build_list`, `build_inspect`: `readOnlyHint=true`
  - `build_prune`: `destructiveHint=true`
- **FR-014**: The audit log MUST record each build invocation (revision,
  duration, result) alongside the existing apply/upgrade events.
- **FR-015**: Concurrent `trond build` invocations against the same cache
  key MUST serialize via a per-key `flock` on
  `${TROND_STATE_DIR}/builds/locks/<key>.lock`. The waiting caller MUST
  return a cache hit after the first completes (not duplicate work).
- **FR-016**: `trond build` MUST handle SIGINT by terminating the build
  container, removing partial output, omitting the manifest entry, and
  exiting 130 with `error_code: "BUILD_CANCELLED"`.
- **FR-017**: `trond preflight --intent <yaml>` MUST, when the intent
  contains a `build:` block, additionally verify: (a) source path exists and
  is a git repo, (b) docker is reachable (or host gradle exists with
  `--builder host`), (c) the builder image is in the local cache or can be
  pulled, (d) for SSH targets, scp is present on the remote.
- **FR-018**: `trond build prune` MUST cross-reference `state.json` and
  refuse to delete any build whose sha equals the `build_revision` field
  of a currently-managed node. The state schema gains an optional
  `build_revision` field on each node entry (additive, MINOR bump).
- **FR-019**: The build environment MUST forward `GRADLE_OPTS` and
  `JAVA_OPTS` from the trond invocation environment into the build
  container. Additional pass-through can be requested via the intent's
  `build.env: { KEY: VALUE }` map (v1 scope: any KEY allowed).

### Key Entities

- **Build**: A content-addressed compilation of a java-tron source tree.
  Properties: source_revision (git sha), patch_hash (if dirty), jdk_version,
  artifact_kind (jar|image), artifact_ref (path or image tag),
  sha256/image_id, duration_ms, builder (docker|host), gradle_task,
  created_at.
- **Source**: A reference to a java-tron checkout. Properties: path
  (canonicalized), revision_spec (HEAD|branch|tag|sha), resolved_revision,
  dirty_state (boolean), patch_hash (when dirty_state).
- **Builder Image Pin**: A frozen mapping `jdk_version → image@sha256:...`
  bundled with each trond release. Refresh path: `make
  refresh-builder-pins`.
- **State node entry** (existing, extended): adds optional
  `build_revision: string` field, populated by apply when the deploy
  consumed a `build:` block.

### Success Criteria

- **SC-001**: A developer with java-tron source on their laptop can run
  `trond apply --intent dev.yaml` and reach a running node in < 5 minutes for
  a cold build, < 1 minute for a cached build.
- **SC-002**: Re-running `trond apply` against an unchanged source tree
  results in `no_change` and exits within 2 seconds.
- **SC-003**: A build can be triggered by an AI agent through MCP (the
  `build` tool) and the agent can chain build → apply → status in one
  conversation.
- **SC-004**: The first 10 trond users to try the dev-loop quickstart (US-1
  / US-2) complete it without manual intervention on the build step.

## Out of Scope (For This Feature)

- Multi-arch image build matrix (`linux/amd64,linux/arm64`). Recorded in
  follow-up. The Phase 4 design notes the buildx hook point.
- Build provenance signing (cosign-signed artifacts at build time). The
  release pipeline in tron-docker already does this; trond's dev builds are
  intentionally unsigned.
- Builds against branches of dependencies (e.g., custom protobuf-java).
- Cross-source builds (combine multiple repos into one artifact).
- Remote-host builds (build *on* the SSH target). Out of scope per
  clarification.
- Image artifact + SSH target combined. Use registry push for that case
  (existing `image:` path).

## Dependencies

- This feature does NOT depend on the toolkit wrapper or analyze layer.
- The shadow-fork feature DOES depend on a working build pipeline (it needs
  to produce a forked-state JAR/image).

## CHANGELOG

- **2026-05-08**: Initial draft.
- **2026-05-08 (self-review)**: Applied 17-item review. Material changes:
  - Removed ambiguous `build.rebuild: always|on_change|never` field.
  - FR-002 patch hash now MUST include untracked files (regression bug fix
    in design).
  - FR-005 makes `pull_policy: never` explicit for local-built images.
  - FR-015 (concurrent lock), FR-016 (SIGINT), FR-017 (preflight), FR-018
    (prune cross-ref state), FR-019 (env passthrough) all newly added.
  - US-1 gained acceptance scenarios 6-7 (SIGINT, concurrent).
  - US-2 gained scenario 4 (pull_policy: never).
  - US-4 gained scenarios 3-4 (progress, scp probe).
  - FR-013 MCP tool annotations now explicit.
