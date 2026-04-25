# TRON Node Types

## FullNode

A FullNode stores the complete blockchain state and validates all transactions and blocks.

- **Use cases**: API provider, dApp backend, block explorer data source, general-purpose node
- **Sync mode**: Syncs from genesis or a snapshot; maintains full state trie
- **Resources**:
  - CPU: 8+ cores recommended (16 cores for high-traffic API)
  - Memory: 32 GB minimum, 48 GB recommended
  - Disk: 1.5 TB+ SSD (grows ~200 GB/year on mainnet)
  - Network: 100 Mbps minimum
- **Default ports**:
  - 18888 — P2P listen port
  - 8090 — HTTP API (fullNodePort)
  - 50051 — gRPC port
  - 9527 — Prometheus metrics

## Witness Node (Super Representative)

A Witness node is a FullNode that also participates in block production. Only the top 27 elected Super Representatives produce blocks; SR Partners and candidates run witness nodes to stay ready.

- **Use cases**: Block production, governance voting participation
- **Resources**:
  - CPU: 16+ cores (block production is latency-sensitive)
  - Memory: 48 GB minimum, 64 GB recommended
  - Disk: 1.5 TB+ SSD with high IOPS (NVMe preferred)
  - Network: 200 Mbps minimum, low-latency peering to other SRs
- **Default ports**: Same as FullNode
- **Additional config**:
  - `node.isWitness = true`
  - Witness private key configured via `localwitness` or keystore
  - Requires stable uptime; missed blocks reduce rewards

## SolidityNode (Deprecated)

SolidityNode served confirmed (solidity) block data via a separate process. It is deprecated since java-tron 4.0.

- **Use cases**: Legacy deployments only
- **Current approach**: FullNode now serves both full and solidity APIs on separate ports
  - 8091 — Solidity HTTP API (solidityPort)
  - 50061 — Solidity gRPC port
- **Do not deploy new SolidityNode instances.** Use FullNode with solidity API enabled.

## Lite FullNode

A Lite FullNode stores only recent state data instead of the full history. It syncs faster and uses less disk.

- **Use cases**: Quick-start environments, development, resource-constrained deployments, read-heavy API nodes that do not need historical state
- **Resources**:
  - CPU: 8+ cores
  - Memory: 16 GB minimum, 32 GB recommended
  - Disk: 500 GB SSD (significantly smaller than full node)
  - Network: 100 Mbps minimum
- **How to run**: Start from a Lite FullNode snapshot (available from official TRON backup sources)
- **Limitations**:
  - Cannot serve historical queries for pruned state
  - Not suitable for block explorers that need full history
  - Cannot be used as a Witness node

## Choosing a Node Type

| Scenario | Recommended Type |
|---|---|
| Production API service | FullNode |
| Block production (SR) | Witness |
| dApp development / testing | Lite FullNode |
| Quick read-only API | Lite FullNode |
| Historical data queries | FullNode |
| New deployment replacing SolidityNode | FullNode with solidity API |

## Port Summary

| Port | Protocol | Purpose |
|---|---|---|
| 18888 | TCP | P2P peer discovery and sync |
| 8090 | HTTP | Full node API |
| 8091 | HTTP | Solidity API (confirmed data) |
| 50051 | gRPC | Full node RPC |
| 50061 | gRPC | Solidity RPC |
| 9527 | HTTP | Prometheus metrics |
