# tron-deployment Development Guidelines

Auto-generated from all feature plans. Last updated: 2026-04-08

## Active Technologies

- Go 1.25+ (validator/v10 dependency) (001-trond-cli-platform)

## Project Structure

```text
tron-deployment/
├── cmd/                    # Cobra CLI commands
├── internal/               # Internal packages
│   ├── intent/             # Intent YAML parsing
│   ├── render/             # Config rendering (HOCON + compose/systemd)
│   ├── runtime/            # Docker and Jar runtime abstractions
│   ├── target/             # Local and SSH target abstractions
│   ├── state/              # State file management
│   ├── security/           # PrivateKey type, audit log
│   ├── diagnosis/          # Health check runners
│   ├── output/             # JSON/text formatters, exit codes
│   ├── paths/              # Centralised state-dir resolution (--state-dir / TROND_STATE_DIR)
│   └── knowledge/          # Embedded knowledge files
├── knowledge/              # Deployment guidance markdown
├── examples/               # Example intent.yaml files
├── schemas/                # JSON Schema for intent validation
├── templates/              # Base java-tron .conf files
├── main_net_config.conf    # Mainnet base config
├── test_net_config.conf    # Nile testnet base config
└── private_net_config.conf # Private network base config
```

## Commands

```bash
make build          # Build trond binary to ./bin/trond
make test           # Run unit tests
make lint           # Run golangci-lint
make e2e            # End-to-end tests (requires Docker)
make build-all      # Cross-compile all platforms
```

## Code Style

Go 1.23+: Follow standard Go conventions (gofmt, golangci-lint).

## Recent Changes

- 001-trond-cli-platform: Redesigning tron-deployment as CLI-first node deployment platform

<!-- MANUAL ADDITIONS START -->
## AI Agent Guide — Using trond

When a user asks to deploy, manage, or diagnose a TRON node, use `trond`:

### Workflow
1. Choose or create an intent file from `examples/`
2. Validate: `trond config validate <intent.yaml>`
3. Preview: `trond plan --intent <intent.yaml> --output json`
4. Deploy: `trond apply --intent <intent.yaml> --output json`
5. Check: `trond status <node-name> --output json`
6. Diagnose: `trond diagnose <node-name> --output json`

### Always use `--output json` and parse the structured response.

### Error handling
Parse the `suggestions[]` array in error output and attempt the first suggestion.

### Key commands
- `trond apply` — idempotent deploy (alias: `deploy`); add `--wait` to block until ready
- `trond plan` — preview changes without deploying
- `trond status` — node state, block height, sync progress
- `trond diagnose` — structured health report with fix suggestions
- `trond knowledge <topic>` — query deployment guidance
- `trond preflight` — check target readiness before deploying

### Test-harness SDK (for programmatic / CI use)
- `trond inspect [--all|--network <p>|<node>]` — JSON manifest of endpoints + container IPs
- `trond exec <node> -- <cmd>` — run a command inside the node
- `trond files put|get <node> <src> <dst>` — push/pull files
- `trond wait <node> --port|--http|--exec` — block until a probe succeeds (supports JSON path assertions)
- `trond disconnect|connect <a> <b>`, `trond partition|heal --groups 'a,b|c,d'` — chaos primitives
- `trond events [--follow] [--since <dur>]` — JSONL audit-log feed
- Use `--state-dir <dir>` or `TROND_STATE_DIR=<dir>` to isolate concurrent enclaves
- Set `auto_ports: true` under `target:` in intent for OS-allocated free ports

### Exit codes
- 0: success
- 1: general error (incl. WAIT_TIMEOUT, EXEC_ERROR, CHAOS_ERROR, NODE_NOT_FOUND)
- 2: validation error (check intent.yaml)
- 3: target unreachable (check SSH/Docker)
- 4: preflight failure (check requirements)
- 10: human required (destructive op needs --confirm or --auto-approve)
<!-- MANUAL ADDITIONS END -->
