# trond - TRON Node Deployment CLI

A command-line tool for deploying, managing, and diagnosing [java-tron](https://github.com/tronprotocol/java-tron) nodes using declarative intent files.

## Features

- **Declarative deployment** -- describe desired state in YAML, `trond` makes it happen
- **Multi-runtime** -- Docker Compose or native Jar + systemd
- **Multi-target** -- local host or remote SSH
- **Idempotent** -- `trond apply` is safe to run repeatedly
- **Plan/Apply workflow** -- preview changes before deploying
- **Structured output** -- JSON output for CI pipelines and AI agents
- **Built-in diagnostics** -- sync health, peer count, disk, ports, version checks
- **Knowledge base** -- embedded deployment guidance and troubleshooting

## Install

### From release

Download the latest binary from [Releases](https://github.com/tronprotocol/tron-deployment/releases):

```bash
# Linux amd64
curl -LO https://github.com/tronprotocol/tron-deployment/releases/latest/download/trond_linux_amd64.tar.gz
tar xzf trond_linux_amd64.tar.gz
sudo mv trond /usr/local/bin/
```

### From source

```bash
git clone https://github.com/tronprotocol/tron-deployment.git
cd tron-deployment
make build
# Binary: ./bin/trond
```

Requires Go 1.25+.

## Quickstart

1. **Create an intent file** (or use an example):

```yaml
name: my-fullnode
network: mainnet
target:
  type: local
  runtime: docker
nodes:
  - type: fullnode
    version: "4.8.1"
```

2. **Validate** the intent:

```bash
trond config validate intent.yaml
```

3. **Preview** changes:

```bash
trond plan --intent intent.yaml
```

4. **Deploy**:

```bash
trond apply --intent intent.yaml --auto-approve
```

5. **Check status**:

```bash
trond status my-fullnode
```

## Commands

### Lifecycle

| Command | Description |
|---|---|
| `trond apply` | Deploy or update a node (alias: `deploy`); supports `--wait` |
| `trond plan` | Preview changes without deploying |
| `trond status <node>` | Show node state, block height, sync progress |
| `trond list` | List all managed nodes |
| `trond stop` / `start` / `restart` `<node>` | Process control |
| `trond remove <node>` | Remove a deployed node |
| `trond upgrade <node>` | Upgrade to a new version (auto-rollback on failure) |
| `trond rollback <node>` | Rollback to previous version |
| `trond logs <node>` | Stream node logs |
| `trond diagnose <node>` | Run structured health checks |
| `trond health <node>` | Quick HTTP API probe |
| `trond verify <node>` | Post-deploy health gate |
| `trond preflight` | Check target readiness |
| `trond bootstrap` | Install Docker or JDK on target |

### Configuration

| Command | Description |
|---|---|
| `trond config validate` | Validate an intent file |
| `trond config render` | Render HOCON config from intent |
| `trond config diff` | Diff rendered vs deployed config |
| `trond config explain <key>` | Explain a HOCON config key |

### Networks

| Command | Description |
|---|---|
| `trond network create` | Create a multi-node private network |
| `trond network add` | Append a node to an existing network |
| `trond network status` | Show private network status |
| `trond network destroy` | Destroy a private network |

### Test-harness SDK

These commands exist so external test tools can drive trond programmatically.

| Command | Description |
|---|---|
| `trond inspect [node \| --network \| --all]` | JSON manifest of endpoints, container IPs, runtime info |
| `trond exec <node> -- <cmd>` | Run a command inside a node (docker exec / host exec) |
| `trond files put <node> <local> <remote>` | Push a file into a node |
| `trond files get <node> <remote> <local>` | Pull a file from a node |
| `trond wait <node> --port \| --http \| --exec` | Block until a probe succeeds |
| `trond disconnect <a> <b>` / `connect` | Tear down / restore peer link at the docker network layer |
| `trond partition --groups 'a,b\|c,d'` / `heal` | Apply / reverse a network partition |
| `trond events [--follow] [--since <dur>]` | Stream the audit log as JSONL |
| `trond knowledge [topic]` | Query embedded deployment guidance |

### Global flags

| Flag / env | Effect |
|---|---|
| `--state-dir <dir>` / `TROND_STATE_DIR` | Relocate `state.json`, `audit.log`, `deployments/` (enables parallel enclaves) |
| `-o, --output text\|json` | Output format (json for machine consumers) |
| `--log-format text\|json` | Log format on stderr |
| `-q, --quiet` / `-v, --verbose` | Output verbosity |
| `--no-color` | Disable ANSI colors |
| `TROND_TEMPLATES_DIR` | Override the embedded HOCON templates |
| `TROND_SSH_ACCEPT_NEW_HOSTS=1` | TOFU mode for SSH host keys (refuses on mismatch — never trusts a key change) |

## Architecture

```
intent.yaml --> [validate] --> [render HOCON + compose/systemd]
                                       |
                               [plan / apply]
                                       |
                        [Docker Compose | Jar + systemd]
                                       |
                              [target: local | SSH]
                                       |
                               [state.json tracking]
```

**Trust ladder** -- progressive levels of automation:

1. `trond config validate` -- just check syntax
2. `trond plan` -- see what would change
3. `trond apply` -- deploy with confirmation gate
4. `trond apply --auto-approve` -- fully automated (CI mode)

## Test-harness Integration

trond is also designed to be **driven by an external test tool** (similar to
how Hive or Kurtosis drive Ethereum clients). The test tool brings the
assertions and scenario logic; trond provides the deployment substrate.

A typical CI scenario:

```bash
JOB=$CI_JOB_ID
T="trond --state-dir /tmp/trond-$JOB"

# 1. Bring up an isolated enclave with auto-allocated ports.
$T apply --intent test/private-3sr.yaml --auto-approve --wait --wait-timeout 5m

# 2. Subscribe to the event stream for the harness's own reporting.
$T events --follow > /tmp/trond-$JOB/events.jsonl &
EVENTS_PID=$!

# 3. Discover the topology.
$T inspect --all -o json > /tmp/trond-$JOB/topology.json

# 4. Inject a fixture and run a scripted scenario.
$T files put sr0 ./fixtures/genesis.conf /etc/tron/config.conf
$T exec fullnode0 -- curl -s http://127.0.0.1:8090/wallet/createaccount

# 5. Simulate a 2/2 SR partition and wait for the network to detect.
$T partition --groups 'sr0,sr1 | sr2,fullnode0'
$T wait fullnode0 --http '{http}/wallet/getnowblock' \
   --json-path block_header.raw_data.number --json-gt 100 --timeout 2m

# 6. Heal and tear down.
$T heal --groups 'sr0,sr1 | sr2,fullnode0'
$T network destroy --confirm test
kill $EVENTS_PID
```

The `--state-dir` plus `auto_ports: true` (in the intent's `target` block)
let multiple CI jobs run in parallel against the same Docker daemon without
colliding. Every command's `-o json` output is structured for downstream
consumption.

## Configuration Templates

Base java-tron configuration templates rendered into per-node HOCON. The
mainnet and Nile templates track upstream — periodically refresh from the
authoritative sources before tagging a release:

| File | Network | Upstream source of truth |
|---|---|---|
| `main_net_config.conf` | Mainnet | https://github.com/tronprotocol/java-tron/blob/develop/framework/src/main/resources/config.conf |
| `test_net_config.conf` | Nile testnet | https://github.com/tron-nile-testnet/nile-testnet/blob/master/framework/src/main/resources/config-nile.conf |
| `private_net_config.conf` | Private network | maintained in-repo (no upstream) |

Refresh from upstream:

```bash
make sync-templates    # fetches mainnet + nile, leaves private alone
```

After a sync, run `make test` and `./bin/trond config validate examples/*.yaml`
to confirm nothing broke. Keep both copies in sync — `templates/<file>` is a
symlink pointing at the root `<file>`, and `internal/render/templates/<file>`
is the embedded copy used at runtime.

## Examples

See `examples/` for sample intent files:

- `mainnet-fullnode.yaml` -- standard mainnet fullnode
- `nile-fullnode.yaml` -- Nile testnet fullnode
- `mainnet-witness.yaml` -- mainnet witness (Super Representative)
- `private-network.yaml` -- multi-node private network
- `remote-ssh-fullnode.yaml` -- deploy to remote host via SSH

## Development

```bash
make build      # Build trond binary
make test       # Run unit tests
make lint       # Run golangci-lint
make e2e        # End-to-end tests (requires Docker)
make build-all  # Cross-compile all platforms
```

## License

See [LICENSE](LICENSE).
