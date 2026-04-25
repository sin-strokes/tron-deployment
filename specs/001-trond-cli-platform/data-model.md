# Data Model: trond CLI Platform

**Feature**: 001-trond-cli-platform
**Date**: 2026-04-08

## Entities

### Intent (User Input)

The declarative file describing desired node state. Written by users or AI agents.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | yes | Unique node identifier within state file |
| target | Target | yes | Where to deploy |
| network | enum | yes | mainnet, nile, private |
| nodes | []NodeSpec | yes | One or more node specifications |

**Validation rules**:
- `name` must match `^[a-z0-9][a-z0-9-]{0,62}$` (DNS-label safe)
- `name` must be unique per state file
- `network` must be one of the defined enum values
- `nodes` must contain at least one entry

### Target

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| type | enum | yes | — | local, ssh |
| host | string | if ssh | — | Hostname or IP |
| user | string | if ssh | — | SSH username |
| port | int | no | 22 | SSH port |
| identity_file | string | no | ~/.ssh/id_rsa | Path to SSH private key |
| runtime | enum | no | docker | docker, jar |

**Validation rules**:
- `host` and `user` required when `type=ssh`
- `identity_file` must be readable if specified

### NodeSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| type | enum | yes | — | fullnode, witness, solidity, lite |
| version | string | no | latest | java-tron release tag |
| image | string | no | tronprotocol/java-tron | Docker image (docker runtime) |
| install_path | string | no | /opt/tron | Installation root (jar runtime) |
| process_manager | enum | no | systemd | systemd, nohup (jar runtime) |
| system_user | string | no | tron | Unix user to run as (jar runtime) |
| witness_key_env | string | no | — | Env var name containing SR private key |
| features | Features | no | {} | Feature flags |
| resources | Resources | no | {} | Resource allocations |
| jvm | JVMConfig | no | auto | JVM tuning overrides |
| ports | PortMapping | no | defaults | Custom port mappings |

**Validation rules**:
- `witness_key_env` required when `type=witness`
- `witness_key_env` must be an env var NAME, not a value (reject if starts with hex/base58)

### Features

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| metrics | bool | false | Enable Prometheus metrics on port 9527 |
| jsonrpc | bool | false | Enable Ethereum-compatible JSON-RPC |
| rate_limit | bool | true | Enable API rate limiting |
| event_subscribe | bool | false | Enable event subscription system |
| pbft | bool | false | Enable PBFT consensus endpoints |

### Resources

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| memory | string | 16GB | Container memory limit or JVM hint |
| storage | string | — | Informational; used by preflight check |

### JVMConfig (optional overrides)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| heap_max | string | auto | -Xmx (auto = calculated from system memory) |
| heap_new | string | auto | -Xmn |
| direct_memory | string | auto | -XX:MaxDirectMemorySize |
| gc | enum | auto | G1, CMS (auto = G1 for JDK17+, CMS for JDK8) |
| gc_log | bool | true | Enable GC logging |

### PortMapping (optional overrides)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| http | int | 8090 | FullNode HTTP API |
| grpc | int | 50051 | FullNode gRPC |
| solidity_http | int | 8091 | Solidity HTTP API |
| solidity_grpc | int | 50061 | Solidity gRPC |
| jsonrpc | int | 8545 | JSON-RPC (if enabled) |
| p2p | int | 18888 | P2P listen port |
| metrics | int | 9527 | Prometheus metrics (if enabled) |

---

## Internal State Entities

### ManagedNode (state.json entry)

| Field | Type | Description |
|-------|------|-------------|
| name | string | User-assigned node name |
| intent_hash | string | SHA256 of the intent file at last apply |
| config_hash | string | SHA256 of the rendered HOCON config |
| version | string | Deployed java-tron version |
| target | Target | Copy of target from intent |
| runtime | enum | docker or jar |
| status | enum | running, stopped, error, unknown |
| last_applied | timestamp | ISO 8601 UTC |
| previous_version | string? | For rollback support |
| compose_path | string? | Path to generated compose file (docker) |
| systemd_unit | string? | Unit name (jar) |
| install_path | string? | Path on target (jar) |

**State transitions**:
```
(none) ──apply──► running
running ──stop──► stopped
stopped ──start──► running
running ──apply(changed)──► running (rolling update)
running ──remove──► (deleted from state)
running ──upgrade──► running (new version)
running ──error detected──► error
error ──apply──► running (converge)
```

### AuditEntry (audit.log line)

| Field | Type | Description |
|-------|------|-------------|
| timestamp | ISO 8601 | When the action occurred |
| command | string | Full trond command invoked |
| node | string? | Node name if applicable |
| target | string | Target description |
| intent_hash | string? | Intent file hash |
| result | enum | success, failure, no_change |
| duration_ms | int | Wall-clock duration |
| error_code | string? | Error code if failed |

Format: One JSON object per line (JSONL), append-only.
