# Implementation Plan: trond Build Pipeline

**Branch**: `feat/build-pipeline` | **Date**: 2026-05-08 | **Spec**: [spec.md](spec.md)

## Summary

Add a `trond build` command and an apply-side `build:` intent block that
together let developers iterate on java-tron source code without context-
switching out to Gradle. The build itself happens in a Docker container
running a pinned `eclipse-temurin` image. trond orchestrates: source mount,
gradle invocation, output capture, content-addressed caching, and (for
SSH targets) artifact transfer.

trond ships no JDK, no Gradle, no Java compiler. The build environment is
the container; trond is the conductor.

## Technical Context

**Language/Version**: Go 1.25+
**New Dependencies** (kept minimal):
- `github.com/go-git/go-git/v5` — resolve `HEAD` / branch / tag → sha,
  detect dirty working tree, compute patch hash. Already considered for
  internal/state but not yet pulled in.
- `archive/zip` (stdlib) — read JAR manifest to validate `Main-Class`.
- `crypto/sha256` (stdlib) — content hashing.

**Existing trond packages reused**:
- `internal/paths` — for `${TROND_STATE_DIR}/builds/`
- `internal/output` — for the structured error envelope
- `internal/state` — to record built artifacts (no new file required;
  reuse audit log)
- `internal/mcp` — to expose tools
- `internal/target/ssh` — for the scp transfer path in US-4

**No removed dependencies**.

## Architecture

```
                          trond CLI
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
   cmd/build.go         cmd/apply.go          internal/mcp
   (standalone)         (resolves build:)     (tool: build)
        │                     │                     │
        └──────── calls ──────┴──────── calls ──────┘
                              │
                  internal/build/builder.go
                  (Builder interface)
                  ┌───────────┴────────────┐
                  │                        │
       internal/build/docker.go   internal/build/host.go
       (containerized)            (--builder host)
                  │
                  └─► docker run eclipse-temurin:8-jdk-jammy@sha256:...
                       -v <source>:/src:ro
                       -v <cache>/gradle:/root/.gradle
                       -v <cache>/out:/out:rw
                       bash -c "./gradlew shadowJar && cp build/libs/*.jar /out/"

                  internal/build/cache.go
                  (content-addressed cache, manifest dir, prune logic)

                  internal/build/validate.go
                  (JAR Main-Class check, image inspect)
```

### Directory layout on disk

```
${TROND_STATE_DIR}/builds/
├── gradle/                 # ~/.gradle persisted across builds
├── out/                    # produced JARs
│   ├── abc123.jar
│   └── abc123+dirty-7f2a.jar
├── images/                 # local image registry (sha → tag map)
│   └── abc123.json
└── manifest/               # one JSON per build, source of truth
    ├── abc123.json
    └── abc123+dirty-7f2a.json
```

The `manifest/` directory is the cache key index. `cache.go` reads only
manifests (small JSON files); the artifacts under `out/` and `images/` are
opaque blobs.

### Cache key derivation

```go
type CacheKey struct {
    SourcePath   string // canonicalized abs path
    GitRevision  string // resolved sha
    PatchHash    string // sha256 of git diff if dirty, else ""
    JDKVersion   string
    ArtifactKind string // "jar" | "image"
}

func (k CacheKey) String() string {
    if k.PatchHash != "" {
        return fmt.Sprintf("%s+dirty-%s", k.GitRevision, k.PatchHash[:8])
    }
    return k.GitRevision
}
```

Two different source paths producing the same sha hit the same cache (this
is the intent — a build is determined by its inputs, not its location).

### Intent integration

The intent schema gains a `build:` block, mutually exclusive with `image:`.

```yaml
name: dev-fullnode
network: nile
node:
  type: fullnode
  runtime: jar               # or docker
target:
  type: local

build:
  source: /Users/me/java-tron
  revision: HEAD             # or branch / tag / sha
  jdk: "8"                   # default
  artifact: jar              # or image
  image_tag: dev:latest      # required if artifact=image
  builder: docker            # default; "host" available
  rebuild: on_change         # always | on_change | never
```

Render pipeline change: if `build:` is present, render runs the build first,
then substitutes the produced artifact ref (JAR path or image tag) into the
runtime config. Render is otherwise unchanged.

### Apply pipeline change

```
preflight → validate intent → [NEW: resolve build] → render → diff → apply
```

`resolve build` is a no-op if the intent has no `build:` block (backwards-
compatible). Otherwise it calls the builder, then mutates the in-memory
intent to substitute the resolved artifact ref. The original intent file is
not touched.

### SSH transfer path (US-4)

When `target.type == ssh` and `build.artifact == jar`:

```
local build → ~/.trond/builds/out/<sha>.jar
       │
       ▼ scp (skip if remote file already matches sha256)
remote:/opt/trond/deployments/<node>/java-tron.jar
       │
       ▼ systemd unit references /opt/trond/deployments/<node>/java-tron.jar
       │
       ▼ systemctl start
```

When `artifact == image` and target is SSH: out of scope for v1. Document
that and require `target.type: local` for image artifacts. Users wanting
remote image deploys should push to a registry and reference via the
existing `image:` path.

## Phase Breakdown

### Phase 1 — `trond build` standalone (~2 days)

**Deliverable**: `trond build --source <path> --artifact jar -o json` works
end-to-end. No intent integration yet.

- `cmd/build.go`: cobra command, flags, JSON output via `output.Result`.
- `internal/build/builder.go`: `Builder` interface, default impl.
- `internal/build/docker.go`: containerized builder using `os/exec` against
  the docker CLI (no docker SDK — match the rest of trond).
- `internal/build/cache.go`: manifest read/write, cache lookup.
- `internal/build/source.go`: go-git wrapper, revision resolution, dirty
  detection.
- `internal/build/validate.go`: JAR `Main-Class` check via `archive/zip`.
- `internal/schema/files/build.schema.json` + `schemas/output/build.schema.json`.
- `internal/schema/embed.go`: bump SchemaVersion to 1.3.0; add entry to
  history comment.
- `internal/schema/version_baseline.json`: regenerate via make target.
- Tests:
  - `cmd/build_test.go`: cobra wiring + JSON shape.
  - `internal/build/builder_test.go`: unit with a fake `dockerRunner`.
  - `internal/build/cache_test.go`: cache hit / dirty key / prune.
  - Golden test: a tiny synthetic source tree produces a deterministic
    manifest (excluding timestamps and durations).

### Phase 2 — Intent integration (~2 days)

**Deliverable**: `trond apply --intent dev.yaml` automatically builds.

- `internal/intent/schema.go`: add `Build` struct, validator rules.
- `schemas/intent.schema.json`: add the `build:` block, document mutual
  exclusion with `image:`.
- `internal/apply/apply.go`: insert `resolveBuild()` between validate and
  render. Resolved artifact ref is held on the in-memory intent.
- `internal/render/`: consume the resolved ref. For `runtime: jar`, point
  the systemd unit at `<sha>.jar`. For `runtime: docker`, use the local
  image tag instead of pulling from a registry.
- `internal/output`: add the `build` block to the apply result JSON.
- Tests:
  - `cmd/apply_build_test.go`: intent with `build:` resolves and applies
    against a stub builder.
  - `examples/dev-local.yaml`: a working example intent.

### Phase 3 — Image artifact (~1 day)

**Deliverable**: `--artifact image` and `runtime: docker` work together.

- Recognize `image` artifact in `docker.go`; invoke `./gradlew :dockerBuild`
  (or `:docker`, depending on tron-docker's task name; needs verification).
- Local image-tag book-keeping: write a `images/<sha>.json` mapping
  `image_id → tag`; remove tag on `prune`.
- `internal/render/docker.go`: when artifact is `image`, use the tag
  directly; skip the `image_pull_policy: always` we otherwise set.
- Tests: round-trip with a minimal Dockerfile-only source tree (a stub
  that doesn't need the full java-tron build).

### Phase 4 — SSH target transfer (~1-2 days)

**Deliverable**: build locally, deploy over SSH.

- `internal/target/ssh/scp.go`: add a `Sha256IfExists(remotePath)` probe
  and a `PutFile(localPath, remotePath)` op.
- `internal/apply/apply.go`: when target is SSH and artifact is JAR, after
  the build call `target.PutFile`. Skip if remote sha256 matches.
- `examples/dev-ssh.yaml`.
- Tests: integration test with an SSH-target container (already used by
  existing e2e suite).

### Phase 5 — Build management commands (~1 day)

**Deliverable**: `trond build list / inspect <sha> / prune --keep N`.

- `cmd/build_list.go`, `cmd/build_inspect.go`, `cmd/build_prune.go`.
- `internal/build/cache.go`: prune logic (LRU by `created_at`, never delete
  the build referenced by any currently-running node).
- MCP tools (`internal/mcp/tools_build.go`): expose `build`, `build_list`,
  `build_inspect`, `build_prune` with the same shape as the existing tool
  registrations. `build` carries `destructiveHint=false` (it writes only
  to the cache), `build_prune` carries `destructiveHint=true`.

### Phase 6 — Docs & quickstart (~0.5 day)

- `specs/002-trond-build-pipeline/quickstart.md` — copy/pasteable dev-loop
  walkthrough.
- README.md `## Dev loop` section linking to quickstart.
- AGENTS.md: add `build` to the read-write tool list, document the
  build-then-apply workflow.

## Total estimate

~7-8 working days from MVP (Phase 1+2) to fully closed loop (Phase 1-6).

## Risks and mitigations

- **Risk**: tron-docker's gradle task layout changes (`shadowJar` vs
  `bootJar` vs `dockerBuild`).
  **Mitigation**: keep the gradle task name configurable via the intent's
  `build.gradle_task` field, with a sensible default. Detect the task list
  with `./gradlew tasks --quiet` on first run and cache.

- **Risk**: builder image pin becomes a security-update blocker.
  **Mitigation**: trond release process includes a `make refresh-builder-pins`
  target that re-resolves Eclipse Temurin tags to current digests.

- **Risk**: the cache directory grows unbounded.
  **Mitigation**: `trond build prune --keep N` is in Phase 5. Documented
  in the quickstart. Add a soft-warn when cache > 5 GB.

- **Risk**: gradle caches inside the container conflict with host gradle
  (when developer also uses `./gradlew` outside trond).
  **Mitigation**: trond mounts a separate `<cache>/gradle` directory rather
  than the host's `~/.gradle`. The container's caches are isolated from
  host. Document this.

## Schema impact

- SchemaVersion: 1.2.0 → **1.3.0** (MINOR: adds new `build` schema, no
  change to existing schemas).
- New file: `schemas/output/build.schema.json`.
- Modified: `schemas/intent.schema.json` (additive: new optional `build:`
  block).
- `internal/schema/version_baseline.json`: regenerate.

## Open questions (to resolve during implementation)

1. Exact gradle task name in current tron-docker: `:shadowJar` or
   `:bootJar`? Verify by inspecting `tools/toolkit/build.gradle`.
2. Should `build.jdk: "8"` be a string or a number in the schema?
   Recommend string to match Maven coords style (`"1.8"` vs `"8"` is also
   ambiguous — pick `"8"`, `"11"`, `"17"`, `"21"`).
3. Whether to expose a `--builder ssh:<host>` (build on a remote build
   server) in the future. Hook is there in the `Builder` interface; no
   implementation in v1.
