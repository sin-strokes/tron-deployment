# Feature Specification: trond Build Pipeline

**Feature Branch**: `feat/build-pipeline`
**Created**: 2026-05-08
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

3. **Given** uncommitted edits in the working tree, **When** the developer
   runs the same command, **Then** trond computes a patch hash, names the
   artifact `abc123+dirty-<patchsha>.jar`, and treats it as a distinct cache
   entry.

4. **Given** the build fails (compile error in user's code), **When** the
   command runs, **Then** trond surfaces a structured error envelope
   (`error_code: "BUILD_FAILED"`, message containing gradle's tail output,
   `suggestions` pointing at common causes) and exits 1.

5. **Given** Docker is not running on the host, **When** the developer
   invokes `trond build` without `--builder host`, **Then** trond exits with
   `error_code: "TARGET_UNREACHABLE"`, exit code 3, and a suggestion to start
   Docker or pass `--builder host`.

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
   rendered docker-compose references the local image tag directly (no
   registry pull).

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
- **Concurrent `trond build` calls on the same revision**: use a file lock on
  the cache directory; second caller waits or returns cache hit.
- **Builder image not present and offline**: surface clear network-error
  message, suggest `--builder host` fallback.
- **Revision is `HEAD` but git working tree is not a git repo**: error
  `error_code: "INVALID_SOURCE"` with suggestion to either commit or pass an
  explicit `--source-id <name>`.

## Requirements

### Functional Requirements

- **FR-001**: trond MUST expose a `trond build` cobra command accepting at
  minimum `--source <path>`, `--artifact <jar|image>`, `--revision <rev>`,
  `--jdk <version>`, `--builder <docker|host>`, `--tag <tag>` (for image).
- **FR-002**: The build cache MUST be content-addressed by source git
  revision. Repeated builds at the same revision MUST be no-ops at the cache
  hit path.
- **FR-003**: Build outputs MUST live under `${TROND_STATE_DIR}/builds/`
  (default `~/.trond/builds/`) and respect `--state-dir` / `TROND_STATE_DIR`.
- **FR-004**: Each completed build MUST produce a JSON manifest matching
  `schemas/output/build.schema.json`.
- **FR-005**: The intent.yaml schema MUST accept a `build:` block that is
  mutually exclusive with `image:` and references source + revision.
- **FR-006**: `trond apply` MUST resolve a `build:` block by invoking the
  build pipeline before the render/deploy phases.
- **FR-007**: A build failure MUST set exit code 1 and `error_code:
  "BUILD_FAILED"`, with the gradle stderr tail in the message.
- **FR-008**: The default builder MUST be `docker`. The host builder MUST be
  available as a flag override.
- **FR-009**: When the target is SSH, trond MUST build locally and transfer
  the JAR via scp. Source code MUST NOT be shipped to the remote.
- **FR-010**: Builds MUST be discoverable: `trond build list`, `trond build
  prune --keep N`, `trond build inspect <sha>`.
- **FR-011**: trond MUST validate the produced artifact is structurally valid
  (JAR contains `org.tron.program.FullNode` main class, or image has runnable
  ENTRYPOINT) before declaring success.
- **FR-012**: The pinned builder image MUST be reproducible: trond ships a
  `builder_image_digests.json` mapping `jdk_version → sha256:...`. Upgrading
  the pin is a trond release change, not a runtime change.
- **FR-013**: All build-related operations MUST be exposed through MCP as
  tools so AI agents can drive the dev loop.
- **FR-014**: The audit log MUST record each build invocation (revision,
  duration, result) alongside the existing apply/upgrade events.

### Key Entities

- **Build**: A content-addressed compilation of a java-tron source tree.
  Properties: source_revision (git sha), patch_hash (if dirty), jdk_version,
  artifact_kind (jar|image), artifact_ref (path or image tag),
  sha256/image_id, duration_ms, builder (docker|host), created_at.
- **Source**: A reference to a java-tron checkout. Properties: path,
  revision_spec (HEAD|branch|tag|sha), resolved_revision, dirty_state.
- **Builder Image Pin**: A frozen mapping `jdk_version → image@sha256:...`
  bundled with each trond release.

### Success Criteria

- **SC-001**: A developer with java-tron source on their laptop can run
  `trond apply --intent dev.yaml` and reach a running node in < 5 minutes for
  a cold build, < 1 minute for a cached build.
- **SC-002**: Re-running `trond apply` against an unchanged source tree
  results in `no_change` and exits within 2 seconds.
- **SC-003**: A build can be triggered by an AI agent through MCP (the
  `build` tool) and the agent can chain build → apply → status in one
  conversation.
- **SC-004**: The first 10 trond users to try the dev-loop quickstart (USX-1
  / USX-2) complete it without manual intervention on the build step.

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

## Dependencies

- This feature does NOT depend on the toolkit wrapper or analyze layer.
- The shadow-fork feature DOES depend on a working build pipeline (it needs
  to produce a forked-state JAR/image).
