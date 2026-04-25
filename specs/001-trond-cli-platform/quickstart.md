# Quickstart: trond CLI Platform

**Feature**: 001-trond-cli-platform
**Date**: 2026-04-08

## Prerequisites

- Go 1.23+ (for building from source)
- Docker Engine 20.10+ and Compose V2 (for docker runtime)
- OR JDK 8+/17+ and systemd (for jar runtime)

## Build from Source

```bash
git clone https://github.com/tronprotocol/tron-deployment.git
cd tron-deployment
make build
# Binary at ./bin/trond
```

## Deploy a Local Mainnet Fullnode (Docker)

```bash
# 1. Validate the example intent
trond config validate examples/mainnet-fullnode.yaml

# 2. Preview what will be deployed
trond plan --intent examples/mainnet-fullnode.yaml

# 3. Deploy
trond apply --intent examples/mainnet-fullnode.yaml

# 4. Check status
trond status my-fullnode --output json

# 5. View logs
trond logs my-fullnode --tail 50

# 6. Stop when done
trond stop my-fullnode
```

## Deploy a Remote Witness Node (Jar + SSH)

```bash
# 1. Check the remote target is ready
trond preflight --intent examples/mainnet-witness.yaml

# 2. Bootstrap JDK if needed
trond bootstrap --intent examples/mainnet-witness.yaml

# 3. Deploy (inject private key via env var)
SR_PRIVATE_KEY=<your-key> trond apply --intent examples/mainnet-witness.yaml

# 4. Verify health
trond verify --intent examples/mainnet-witness.yaml --timeout 10m

# 5. Diagnose issues
trond diagnose my-witness --output json
```

## Create a Private Development Network

```bash
trond network create --intent examples/private-network.yaml
trond network status --output json
# ... develop and test ...
trond network destroy --confirm private-dev
```

## Development Workflow

```bash
make test          # Run unit tests
make lint          # Run golangci-lint
make e2e           # Run end-to-end tests (requires Docker)
make build-all     # Cross-compile all platforms
```

## Key Files for Contributors

| Path | Purpose |
|------|---------|
| cmd/ | Cobra command implementations (one file per command) |
| internal/intent/ | Intent YAML parsing and validation |
| internal/render/ | HOCON config + compose/systemd rendering |
| internal/runtime/ | Docker and Jar runtime abstractions |
| internal/target/ | Local and SSH target abstractions |
| internal/state/ | State file and locking |
| internal/security/ | PrivateKey type, SSH whitelist, audit log |
| knowledge/ | Bundled guidance files for `trond knowledge` |
| examples/ | Example intent files for all scenarios |
| templates/ | Base java-tron .conf files for rendering |
