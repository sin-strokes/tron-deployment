# Implementation Plan: trond Build Pipeline

**Branch**: `feat/build-pipeline` | **Date**: 2026-05-08 | **Spec**: [spec.md](spec.md)
**Last revised**: 2026-05-08 (self-review pass)

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

**New dependencies**: **None.** All external interactions go through
`os/exec`, matching the rest of trond:

- `git`: shell out via `exec.CommandContext(ctx, "git", ...)` — `rev-parse`,
  `status --porcelain -uall`, `diff`. Avoids ~30 MB `go-git/v5` impact on
  binary size; consistent with how trond already drives `docker` and `scp`.
- `docker`: existing pattern.
- `scp` / `ssh`: existing `internal/target/ssh`.

**Existing trond packages reused**:
- `internal/paths` — for `${TROND_STATE_DIR}/builds/`
- `internal/output` — for the structured error envelope
- `internal/state` — extends node entry with optional `build_revision`
- `internal/mcp` — to expose tools (`build`, `build_list`, `build_inspect`,
  `build_prune`)
- `internal/target/ssh` — for the scp transfer path in US-4 and the
  `scp` preflight probe (FR-017)
- `internal/audit` — to record build events

**Stdlib only**:
- `archive/zip` — read JAR manifest to validate `Main-Class`.
- `crypto/sha256` — content hashing and patch hashing.
- `os/signal` — SIGINT handling (FR-016).
- `syscall.Flock` — concurrent build serialization (FR-015).

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
                  (Builder interface + docker/host impls in one file)
                              │
                  ┌───────────┼───────────┐
                  │           │           │
                  ▼           ▼           ▼
              docker run    ./gradlew    cache hit
              eclipse-      (host         (manifest
              temurin       builder)      lookup)
                  │
                  └─► -v <source>:/src:ro
                       -v <cache>/gradle:/root/.gradle
                       -v <cache>/out:/out:rw
                       -e GRADLE_OPTS, JAVA_OPTS, ORG_GRADLE_PROJECT_* (allowlist only)
                       --workdir /src
                       ./gradlew <task> <args...>
                       (NO `bash -c`; argv-form only — FR-022)

                  internal/build/cache.go
                  (content-addressed cache, manifest dir, prune logic,
                   flock-based concurrent-build serialization)

                  internal/build/validate.go
                  (JAR Main-Class check, image inspect)

                  internal/build/source.go
                  (shells out to git: rev-parse, status, diff, patch hash)
```

Note: dropped the separate `host.go` file from the v1 draft — host builder
is a single function with a switch in `builder.go`. Avoids over-stratifying
~30 lines of logic.

### Directory layout on disk

```
${TROND_STATE_DIR}/builds/
├── gradle/                 # gradle deps cache, persisted across builds
├── out/                    # produced JARs (named by cache key)
│   ├── abc123.jar
│   └── abc123+dirty-7f2a.jar
├── images/                 # local image registry (sha → tag map)
│   └── abc123.json
├── manifest/               # one JSON per build, source of truth
│   ├── abc123.json
│   └── abc123+dirty-7f2a.json
└── locks/                  # flock per cache key (FR-015)
    └── abc123.lock
```

The `manifest/` directory is the cache key index. `cache.go` reads only
manifests (small JSON files); the artifacts under `out/` and `images/` are
opaque blobs.

### Cache key derivation

```go
type CacheKey struct {
    SourcePath          string // canonicalized abs path (symlinks resolved)
    GitRevision         string // resolved sha
    PatchHash           string // sha256(git diff || git status --porcelain -uall) if dirty
    BuilderImageDigest  string // sha256:... of the JDK image actually used
    JDKVersion          string
    ArtifactKind        string // "jar" | "image"
    GradleTask          string // "shadowJar" | "dockerBuild" | custom
    GradleArgs          []string
}

func (k CacheKey) String() string {
    bd := k.BuilderImageDigest[7:13] // strip "sha256:" then take 6 hex chars
    base := fmt.Sprintf("%s-b%s", k.GitRevision, bd)
    if k.PatchHash != "" {
        return fmt.Sprintf("%s+dirty-%s", base, k.PatchHash[:8])
    }
    return base
}
```

**Critical**:
- PatchHash combines BOTH `git diff` AND `git status --porcelain -uall`.
  Diff alone misses untracked files (FR-002, regression bug found in
  pass 1).
- BuilderImageDigest is in the key (FR-002, found in pass 2). When a
  trond release bumps the JDK pin, every cache entry is automatically
  invalidated — no manual prune required.
- GradleArgs participates (different `-Dflag` produces different bytes).

Two different source paths producing the same sha hit the same cache
(this is the intent — a build is determined by its inputs, not its
location).

### Artifact naming pattern on disk

```
~/.trond/builds/out/<git-sha>-b<digest6>[+dirty-<patchsha8>].jar
                       ^^^^^^^   ^^^^^^^^
                       6 hex     6 hex prefix of patch sha
                       prefix    when working tree dirty
                       of image
                       sha256
```

Example: `8f4e2a-bd4e2a1+dirty-7f2a3b9c.jar` reads as
"revision 8f4e2a, built by image digest starting d4e2a1, with dirty patch
7f2a3b9c". Long enough for collision safety, short enough to grep.

### Concurrent build serialization (FR-015)

```go
// before any expensive work
lockPath := filepath.Join(cacheDir, "locks", key.String()+".lock")
f, _ := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
defer f.Close()
syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

// re-check cache after acquiring lock
if hit := cacheLookup(key); hit != nil {
    return hit, nil   // first caller finished while we waited
}
// otherwise do the build
```

### SIGINT handling (FR-016)

`signal.NotifyContext` is installed once at the `apply` (or `build`)
entry point and threaded through every subprocess that runs after it —
docker, git, ssh, scp. Each invocation uses `exec.CommandContext`, so
cancellation propagates as SIGKILL to the child.

```go
ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
defer cancel()

// build phase
cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--name", containerName, ...)
defer cleanup(containerName) // best-effort `docker kill` + `rm -f out/*.tmp`

if err := cmd.Run(); err != nil {
    if errors.Is(ctx.Err(), context.Canceled) {
        return errResult("BUILD_CANCELLED", 130, err, suggestions...)
    }
    return errResult("BUILD_FAILED", 1, err, suggestionsFromTail(cmd))
}

// transfer phase (SSH target) — same ctx, same cancellation semantics
//   - scp writes to `<remote>.tmp`, rename only on clean exit
//   - on ctx cancel: trond runs `ssh remote 'rm -f <remote>.tmp'` best-effort
```

Writing output via a `.tmp` suffix and rename-on-success guarantees that
a cancelled build/transfer leaves no half-written file pretending to be
a finished artifact — neither locally nor on the SSH target.

### Security boundary: subprocess invocation

A user-controlled intent file (or a `--gradle-task` CLI flag) MUST NOT
be able to inject shell metacharacters into the build pipeline. Two
guardrails (FR-022):

1. **Argv-only**. Every subprocess goes through `exec.Command(name,
   args...)` with no intervening shell. No `bash -c`, no
   `sh -c "..."`. The container's entrypoint stays `./gradlew`
   directly.

   ```go
   // GOOD
   cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
       "-v", srcMount, "-v", outMount, "--workdir", "/src",
       imageRef,
       "./gradlew", task, gradleArgs...)

   // BAD — would have enabled $(...) and ; injection
   cmd := exec.CommandContext(ctx, "bash", "-c",
       fmt.Sprintf("docker run ... %s && cp ...", task))
   ```

2. **Token-regex validation at parse time**. `gradle_task` and each
   `gradle_arg` MUST match `^[a-zA-Z0-9._:=/+-]+$`. Names like
   `shadowJar`, `:dbfork:build`, `-Dorg.gradle.daemon=false`,
   `--offline`, `-Pversion=1.2.3` all pass. Anything with whitespace,
   semicolons, redirection, backticks, `$()`, or quotes is rejected as
   `VALIDATION_ERROR` before docker is even invoked.

The same gate guards `build.env` values (FR-019 allowlist).

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
  gradle_task: shadowJar     # default depends on artifact
  gradle_args:               # extra gradle args (FR-001, FR-022)
    - --no-daemon
    - -Dorg.gradle.parallel=true
  env:                       # allowlisted env only (FR-019)
    GRADLE_OPTS: "-Xmx2g"    # GRADLE_OPTS|JAVA_OPTS|GRADLE_USER_HOME|MAVEN_OPTS|ORG_GRADLE_PROJECT_*
  cache:
    dirty_ttl: 7d            # FR-025; "never" allowed
```

Note: `rebuild: always|on_change|never` is **removed** from the v1 draft.
The cache key derivation already handles every legitimate case (dirty tree
forces a new key; clean tree hits cache).

Render pipeline change: if `build:` is present, render runs the build first,
then substitutes the produced artifact ref (JAR path or image tag) into the
runtime config. Render is otherwise unchanged.

### Pull policy for local images (US-3 acceptance #2, FR-005)

When `artifact: image` is consumed by `runtime: docker`, the rendered
service block MUST carry `pull_policy: never` (Compose 3.9+) so compose
does not attempt to pull the locally-built tag:

```yaml
services:
  java-tron:
    image: trond-build:abc123
    pull_policy: never
```

### Apply pipeline change

```
preflight → validate intent → [NEW: resolve build] → render → diff → apply
```

`resolve build` is a no-op if the intent has no `build:` block (backwards-
compatible). Otherwise it calls the builder, then mutates the in-memory
intent to substitute the resolved artifact ref. The original intent file is
not touched. On success, apply records `build_revision = <key.String()>` on
the state node entry (FR-018), enabling `build prune` to skip in-use builds.

### Preflight integration (FR-017)

When intent has `build:`:

```go
// internal/preflight/build.go (new file)
- check source path exists + `git status` works
- if builder == docker: check docker daemon reachable + builder image cached or pullable
- if builder == host:   check `./gradlew --version` works
- if target == ssh:     ssh probe `command -v scp` on remote
```

Surfaces as preflight checks with the existing pass/warning/fail shape.

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

Transfers > 50 MB emit MCP progress notifications (FR-009) using the same
mechanism `snapshot download` uses. The scp invocation pipes its `-v`
output through a parser that converts byte counts to MCP `progress`
messages.

When `artifact == image` and target is SSH: out of scope for v1. Document
that and require `target.type: local` for image artifacts. Users wanting
remote image deploys should push to a registry and reference via the
existing `image:` path.

## Phase Breakdown

### Phase 1 — `trond build` standalone (~3 days)

**Deliverable**: `trond build --source <path> --artifact jar -o json` works
end-to-end. No intent integration yet.

- `cmd/build.go`: cobra command, flags (`--gradle-task`, `--gradle-arg`,
  `--builder-image-override`, etc.), JSON output via `output.Result`.
- `internal/build/builder.go`: `Builder` interface, default impl, docker
  and host paths inline. Argv-only subprocess invocation (FR-022).
- `internal/build/cache.go`: manifest read/write, cache lookup that also
  stats the artifact file (FR-020), flock serialization (FR-015),
  builder-digest-aware key (FR-002).
- `internal/build/source.go`: shell-out wrapper for `git rev-parse`,
  `status --porcelain -uall`, `diff`; patch hash combining both (FR-002).
- `internal/build/validate.go`: JAR `Main-Class` check via `archive/zip`,
  argv token regex `^[a-zA-Z0-9._:=/+-]+$` for gradle task + args
  (FR-022), env-key allowlist (FR-019).
- `internal/build/signal.go`: SIGINT context propagation + cleanup
  (FR-016).
- `internal/build/audit.go`: emits the FR-023 build-event JSON into the
  existing `internal/audit` log.
- `internal/build/pins.go`: `go:embed builder_image_digests.json`
  (FR-024), with `--builder-image-override` resolver. Override values
  feed into the cache key.
- `internal/schema/files/build.schema.json` + `schemas/output/build.schema.json`.
- `internal/schema/embed.go`: bump SchemaVersion to 1.3.0; add entry to
  history comment.
- `internal/schema/version_baseline.json`: regenerate via make target.
- `builder_image_digests.json` checked in at the repo root (also embedded
  via go:embed); `Makefile :: refresh-builder-pins` regenerates it (FR-012).
- Tests:
  - `cmd/build_test.go`: cobra wiring + JSON shape.
  - `internal/build/builder_test.go`: unit with a fake `dockerRunner`;
    asserts argv form and the absence of any shell invocation.
  - `internal/build/cache_test.go`: cache hit / dirty key including
    untracked files / cache miss when artifact deleted (FR-020) / cache
    invalidation on builder-digest change / prune / concurrent flock.
  - `internal/build/source_test.go`: patch hash regression test for
    untracked file invalidation.
  - `internal/build/signal_test.go`: SIGINT mid-build cleanup; asserts
    `out/*.tmp` removed.
  - `internal/build/validate_test.go`: token regex rejects `;`, ``` ` ```,
    `$()`, whitespace, etc.; env-allowlist rejects `LD_PRELOAD`,
    `PATH`, etc.
  - Golden test: a tiny synthetic source tree produces a deterministic
    manifest (excluding `duration_ms` and `created_at`).
  - **Integration test (build-tag gated)**: `internal/build/integration_test.go`
    (`//go:build integration`) runs a real `eclipse-temurin:8-jdk` against
    a 10-line hello-world gradle project, asserts the produced JAR is
    structurally valid. Runs in CI on the e2e job; skipped on `go test ./...`.

### Phase 2 — Intent integration (~2 days)

**Deliverable**: `trond apply --intent dev.yaml` automatically builds.

- `internal/intent/schema.go`: add `Build` struct, validator rules
  (mutual exclusion with `image:`, valid jdk versions, etc.).
- `schemas/intent.schema.json`: add the `build:` block, document mutual
  exclusion with `image:`.
- `internal/apply/apply.go`: insert `resolveBuild()` between validate and
  render. Resolved artifact ref is held on the in-memory intent. On
  success record `build_revision` on the state node entry (FR-018).
- `internal/state/state.go`: extend node entry with optional
  `build_revision`. SchemaVersion stays 1.3.0 (still MINOR; additive).
- `internal/render/`: consume the resolved ref. For `runtime: jar`, point
  the systemd unit at `<sha>.jar`. For `runtime: docker`, use the local
  image tag with `pull_policy: never` (FR-005).
- `internal/output`: add the `build` block to the apply result JSON.
- `internal/preflight/build.go`: build-related preflight checks (FR-017).
- Tests:
  - `cmd/apply_build_test.go`: intent with `build:` resolves and applies
    against a stub builder.
  - `cmd/preflight_build_test.go`: each FR-017 check surfaces correctly.
  - `examples/dev-local.yaml`: a working example intent.

### Phase 3 — Image artifact (~1 day)

**Deliverable**: `--artifact image` and `runtime: docker` work together.

- Recognize `image` artifact in `builder.go`; invoke `./gradlew :dockerBuild`
  (or whatever `build.gradle_task` overrides to).
- Local image-tag book-keeping: write a `images/<sha>.json` mapping
  `image_id → tag`; remove tag on `prune`.
- `internal/render/docker.go`: when artifact is `image`, use the tag
  directly with `pull_policy: never`.
- Tests: round-trip with a minimal Dockerfile-only source tree (a stub
  that doesn't need the full java-tron build).

### Phase 4 — SSH target transfer (~2 days)

**Deliverable**: build locally, deploy over SSH.

- `internal/target/ssh/scp.go`: add a `Sha256IfExists(remotePath)` probe;
  a `PutFile(ctx, localPath, remotePath)` op that:
  - Writes to `<remotePath>.tmp` first.
  - Pipes `-v` (or `-p` byte counts) through a parser that converts to
    MCP `progress` notifications for transfers > 50 MB (FR-009).
  - On ctx cancel: tries `ssh remote 'rm -f <remotePath>.tmp'`
    best-effort, then returns `BUILD_CANCELLED` (FR-016 SSH branch).
  - On success: atomic rename to `<remotePath>`.
- `internal/target/ssh/preflight.go`: add `command -v scp` probe (FR-017).
- `internal/apply/apply.go`: when target is SSH and artifact is JAR, after
  the build call `target.PutFile`. Skip if remote sha256 matches.
- `examples/dev-ssh.yaml`.
- Tests:
  - Integration test against an SSH-target container (already used by
    existing e2e suite). Asserts the `.tmp`-then-rename pattern, the
    progress notification firing for a synthesized large file, and the
    `command -v scp` preflight check.
  - SIGINT during transfer leaves no `<remotePath>.tmp` on the remote.

### Phase 5 — Build management commands & MCP (~1 day)

**Deliverable**: `trond build list / inspect <sha> / prune --keep N` + MCP
tools surfaced.

- `cmd/build_list.go`, `cmd/build_inspect.go`, `cmd/build_prune.go`.
- `internal/build/cache.go`: prune logic — LRU by `created_at`,
  cross-references `state.json::nodes[].build_revision` to refuse deletion
  of in-use builds (FR-018). Dirty-build entries (those with `+dirty-`
  suffix) are pruned more aggressively (configurable `build.cache.dirty_ttl`
  / `--cache-dirty-ttl`, default 7 days, `never` accepted — FR-025).
  During prune, manifest entries pointing at missing artifacts (e.g., user
  manually deleted a JAR) are also removed (FR-020 cleanup branch).
- MCP tools (`internal/mcp/tools_build.go`): expose `build`, `build_list`,
  `build_inspect`, `build_prune` with annotations per FR-013:
  - `build`: `idempotentHint=true`, `destructiveHint=false`
  - `build_list`, `build_inspect`: `readOnlyHint=true`
  - `build_prune`: `destructiveHint=true`

### Phase 6 — Docs & quickstart (~0.5 day)

- `specs/002-trond-build-pipeline/quickstart.md` — copy/pasteable dev-loop
  walkthrough.
- README.md `## Dev loop` section linking to quickstart.
- AGENTS.md: add `build` to the read-write tool list, document the
  build-then-apply workflow.

## Total estimate

~9-10 working days from MVP (Phase 1+2) to fully closed loop (Phase 1-6).
Revised up from the v1 draft's 7-8 days after the self-review added
non-trivial work to Phase 1 (signal handling, flock, patch hash bug fix)
and Phase 4 (progress notifications, scp probe).

## Risks and mitigations

- **Risk**: tron-docker's gradle task layout changes (`shadowJar` vs
  `bootJar` vs `dockerBuild`).
  **Mitigation**: `build.gradle_task` field in intent + `--gradle-task`
  CLI flag (FR-001). Sensible default per artifact kind.

- **Risk**: builder image pin becomes a security-update blocker.
  **Mitigation**: `make refresh-builder-pins` regenerates digests; CI runs
  it weekly and opens a PR if drift is detected. Users in a bind can pass
  `--builder-image-override <ref>` (escape hatch, not promoted in docs).

- **Risk**: the cache directory grows unbounded (a working dev produces a
  dirty build every few minutes).
  **Mitigation**: `prune` exists in Phase 5. Dirty-build entries have a
  default 7-day TTL (more aggressive than clean-build LRU). Soft-warn when
  cache > 5 GB at the start of any `build`.

- **Risk**: gradle caches inside the container conflict with host gradle
  (when developer also uses `./gradlew` outside trond).
  **Mitigation**: trond mounts a separate `<cache>/gradle` directory rather
  than the host's `~/.gradle`. The container's caches are isolated from
  host. Document this.

- **Risk**: Patch hash misses some signal that changes the build output
  (e.g., file mode changes, submodule state).
  **Mitigation**: `git status --porcelain -uall` covers untracked files,
  modified mode bits, and submodule state. Stays a strict subset of "what
  gradle actually depends on", but it's the strictest off-the-shelf
  hash we can get without parsing build.gradle.

- **Risk**: SSH transfer over a slow link with no progress feedback feels
  hung.
  **Mitigation**: MCP progress notifications for > 50 MB (FR-009). In
  `-o text` mode the same parser drives a tty progress bar.

## Schema impact

- SchemaVersion: 1.2.0 → **1.3.0** (MINOR: adds new `build` schema +
  extends state node entry with optional `build_revision`; no breaking
  changes to existing schemas).
- New file: `schemas/output/build.schema.json`.
- Modified: `schemas/intent.schema.json` (additive: new optional `build:`
  block).
- Modified: `schemas/state.schema.json` (additive: optional
  `build_revision` field).
- `internal/schema/version_baseline.json`: regenerate.

## Open questions (to resolve during implementation)

1. Exact gradle task name in current tron-docker: `:shadowJar` or
   `:bootJar`? Verify by inspecting `tools/toolkit/build.gradle` and
   `tools/dbfork/build.gradle`. The `build.gradle_task` field neutralizes
   this concern; the question is what default to ship.
2. `build.jdk` schema type: string (`"8"`, `"11"`, `"17"`, `"21"`).
   Confirmed — string. Number is ambiguous (`8` vs `1.8`).
3. Whether to expose a `--builder ssh:<host>` (build on a remote build
   server) in the future. Hook is there in the `Builder` interface; no
   implementation in v1.
4. Should `build.env` allow arbitrary keys, or whitelist? v1: arbitrary.
   If supply-chain concerns arise, narrow later.

## CHANGELOG

- **2026-05-08**: Initial draft.
- **2026-05-08 (self-review)**: Applied 17-item review.
  - Removed `go-git/v5` dependency; everything shells out via os/exec.
  - Phase 1 estimate 2 days → 3 days (added signal handling, flock,
    patch hash bug fix).
  - Total estimate 7-8 → 9-10 days.
  - Removed separate `host.go` file from architecture; folded into
    `builder.go`.
  - Architecture diagram redrawn to show new components: flock,
    SIGINT handler, env passthrough, preflight integration.
  - Cache key now explicitly combines git-diff AND git-status (untracked
    files invalidate cache).
  - Added FR-015 (concurrent lock), FR-016 (SIGINT), FR-017
    (preflight), FR-018 (prune state cross-ref), FR-019 (env
    passthrough) — all phases updated accordingly.
  - `pull_policy: never` made explicit for local-built docker images.
  - SSH progress notifications for transfers > 50 MB.
- **2026-05-08 (self-review pass 2)**: Applied 12-item second review.
  - **Security hardening**: dedicated "Security boundary" section; argv-
    only subprocess invocation (no `bash -c`); FR-022 token regex on
    gradle task + args; FR-019 narrows `build.env` to a fixed allowlist
    (`GRADLE_OPTS|JAVA_OPTS|GRADLE_USER_HOME|MAVEN_OPTS|ORG_GRADLE_PROJECT_*`).
  - **Cache correctness**: cache key now includes
    `BuilderImageDigest` + `GradleArgs` (FR-002 pass 2). On-disk naming
    `<sha>-b<digest6>[+dirty-<patchsha8>]`. Cache lookup also stats the
    artifact (FR-020) so a manually-deleted JAR triggers a real rebuild.
  - **Distribution**: `builder_image_digests.json` is now `go:embed`-ed
    (FR-024); `--builder-image-override` is documented as escape hatch
    only and participates in the cache key.
  - **SIGINT extended to SSH** (FR-016): scp writes `.tmp` and renames;
    cancellation tries best-effort remote cleanup.
  - **Audit log shape**: FR-023 fixes a concrete event JSON for build
    operations.
  - **Source path resolution**: FR-021 distinguishes CLI (CWD) vs intent
    (intent-file dir) relative-path resolution.
  - **Preflight**: FR-017 builder-image probe is now offline-friendly
    (warning on missing-and-offline, not hard fail).
  - **New flags**: `--gradle-arg` repeatable (FR-001); cache dirty TTL
    via `build.cache.dirty_ttl` or `--cache-dirty-ttl` (FR-025, default
    7d, `never` accepted).
  - **Integration test**: build-tag gated test runs real
    `eclipse-temurin:8-jdk` against a hello-world gradle project.
  - Phase / total estimate unchanged at ~9-10 days; pass 2 fixes are
    additions to existing phases, not new phases.
