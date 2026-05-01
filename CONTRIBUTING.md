# Contributing to tron-deployment / trond

Thanks for considering a contribution. This repo houses two artefacts:

1. **Curated HOCON config templates** (`*_config.conf`) — synchronised from
   upstream `java-tron` and `nile-testnet`. Don't hand-edit the synced
   parts; refresh via `make sync-templates`.
2. **`trond`, the CLI** — Go code under `cmd/` and `internal/`.

Both ship from the same repo / same release, so PRs to either are welcome.

## Quick start

```bash
git clone https://github.com/tronprotocol/tron-deployment.git
cd tron-deployment
make build            # → bin/trond
make test             # unit tests
make lint             # golangci-lint (project-local install)
make e2e              # end-to-end tests (requires Docker)
```

You don't need Go pre-installed. The first `make` invocation downloads
the pinned Go toolchain (currently 1.25.9) into `./.go-toolchain/` after
SHA256-verifying the upstream archive, caches modules under `./.gopath/`,
and builds everything with that. Nothing touches `~/go`, `/usr/local/go`,
or your system Go install. Re-runs reuse the cached toolchain.

If you'd rather use a Go you've already installed (and accept that your
binary may not be byte-identical to a fresh-clone build), pass
`USE_SYSTEM_GO=1`:

```bash
make USE_SYSTEM_GO=1 build
```

Reclaim every byte:

```bash
make clean-all
```

When bumping the pinned Go version, three places stay in sync:

1. `GO_VERSION` in `Makefile`
2. The four `expected_sha=` lines in `scripts/bootstrap-go.sh`
3. The `toolchain go1.X.Y` directive in `go.mod` (so users on system Go
   auto-fetch the same version when running `go build` directly)

Hashes come from `https://go.dev/dl/?mode=json` — never from a
third-party mirror. A one-liner for collecting them:

```bash
curl -sL "https://go.dev/dl/?mode=json" | \
  python3 -c 'import sys,json; [print(f["filename"], f["sha256"]) \
    for v in json.load(sys.stdin) if v["version"]=="go1.X.Y" \
    for f in v.get("files",[]) if f["os"] in ("linux","darwin") \
    and f["arch"] in ("amd64","arm64") and f["kind"]=="archive"]'
```

## Filing an issue

Use the templates in `.github/ISSUE_TEMPLATE/`:

- **Bug** — include `trond version`, `trond doctor`, the failing
  command + `-o json` output, and reproduction steps.
- **Feature** — describe the use case, the user story, and (if you've
  thought about it) the proposed CLI surface.
- **Question** — for usage questions; check `trond knowledge` topics first.

## Submitting a PR

1. **Branch from `develop`**, not `master`. Releases are cut from `master`.
2. Keep changes focused — one logical concern per PR.
3. Run `make build test lint` before pushing.
4. For new commands or fields, add:
   - the field to `internal/intent/schema.go` + `schemas/intent.schema.json`
   - a test case in `internal/intent/fields_test.go`
   - the `--help` text and a brief mention in `README.md` "Intent Reference"
5. The PR template will ask for verification steps; fill them in.

## Commit messages

Imperative mood, present tense, body explains the *why* (not the what — diff
shows that). Example:

```
Inline witness private key into rendered HOCON

typesafe-config does not perform ${ENV} substitution; the literal
"${SR_KEY}" was being read as a 9-character string and the witness
shut down with WITNESS_INIT(1).
```

Don't squash on merge unless asked — the maintainer prefers seeing the
intermediate commits during code review.

## Code style

- `gofmt` (enforced)
- `go vet` clean (enforced)
- `golangci-lint` issues from `.golangci.yml` should be addressed; if the
  rule is wrong, exclude with a comment explaining why.
- Comments belong on the *why*, not the *what*. Self-explanatory code
  doesn't need a docstring; one-line "WHY" comments are great.

## Adding a new intent field

The full surface lives in three places that must agree:

1. `internal/intent/schema.go` — the Go struct
2. `schemas/intent.schema.json` — the JSON Schema (consumed by IDEs)
3. `README.md` "Intent Reference" — the human-readable form

Plus:
4. **Render logic** — wire it into `internal/render/{hocon,compose,systemd}.go`
   if it produces config output
5. **State** — if the field needs to survive across CLI sessions
   (e.g. for `inspect`/`status`), add it to `internal/state/types.go` and
   populate at apply time
6. **Test** — at least one entry in `internal/intent/fields_test.go`

## Licensing

By contributing, you agree your code is published under the same MIT license
as the rest of the project (see `LICENSE`).

## Security disclosure

Don't open public issues for security vulnerabilities. Email
**security@tron.network** — see `SECURITY.md` for details.
