# java-tron HOCON Config Reference

Important configuration keys for java-tron `.conf` files. Format is HOCON (Human-Optimized Config Object Notation), a superset of JSON.

## Network Identity

- **`net.type`** = `mainnet` -- Network type. Must match the target network.
- **`node.p2p.version`** = `11111` -- Protocol version for peer handshake. Nodes only connect to peers with the same version. Mainnet and Nile testnet use different values. Private networks should use a unique value.

## P2P Networking

- **`node.listen.port`** = `18888` -- TCP port for P2P connections. Change if running multiple nodes on one host.
- **`node.maxActiveNodes`** = `30` -- Max simultaneous peer connections. Increase for better sync (costs bandwidth).

- **`seed.node`** -- Initial peer discovery list:
  ```
  seed.node {
    ip.list = ["3.225.171.164:18888", "52.53.189.99:18888"]
  }
  ```
  Use official seed lists for mainnet/testnet. For private networks, list all known peers.

- **`node.active`** -- Force persistent connections to specific peers. Useful for private networks or ensuring connectivity to trusted peers. Format: list of `"ip:port"` strings.

## API Ports

- **`node.http.fullNodePort`** = `8090` -- HTTP API for full node endpoints (`/wallet/*`)
- **`node.http.solidityPort`** = `8091` -- HTTP API for confirmed-only endpoints (`/walletsolidity/*`)
- **`node.rpc.port`** = `50051` -- gRPC full node RPC
- **`node.rpc.solidityPort`** = `50061` -- gRPC solidity RPC

## Witness Configuration

- **`localwitness`** = `["private-key-hex"]` -- Private key for block production. Only on Witness nodes. For better security, use keystore instead.
- **`localwitnesskeystore`** = `["keystore-file-path"]` -- Encrypted keystore alternative to plaintext `localwitness`. Requires password on startup.
- **`block.needSyncCheck`** = `true` -- Sync with network before producing blocks. Set `false` only for the first witness bootstrapping a new private network.

## Storage

- **`storage.db.directory`** = `"database"` -- Subdirectory name for the database within the output directory.
- **`storage.db.engine`** = `"LEVELDB"` -- Database engine. Options: `LEVELDB` (default, most tested) or `ROCKSDB` (may offer better performance, requires native library).
- **`storage.index.directory`** = `"index"` -- Subdirectory for the index database.

## Metrics and Monitoring

```
node.metrics {
  prometheus {
    enable = true
    port = 9527
  }
}
```

Enable Prometheus metrics at `http://<host>:9527/metrics`.

## Committee Parameters

Governance values set via SR proposals. Relevant for private network genesis configuration; cannot be overridden on public networks.

- `committee.allowCreationOfContracts` -- Enable smart contract deployment
- `committee.allowMultiSign` -- Enable multi-signature accounts
- `committee.allowTvmTransferTrc10` -- Enable TRC-10 transfers in TVM
- `committee.allowTvmConstantinople` -- Enable Constantinople TVM features
- `committee.allowTvmSolidity059` -- Enable Solidity 0.5.9 compatibility

## VM Settings

- **`vm.supportConstant`** = `true` -- Enable read-only smart contract calls via API. Recommended for API nodes serving dApps. Disabled by default.
- **`vm.minTimeRatio`** / **`vm.maxTimeRatio`** -- CPU time limits for contract execution. Rarely changed outside testing. Defaults: `0.0` / `5.0`.

## Example Minimal FullNode Config

```
net.type = mainnet
node.p2p.version = 11111
node.listen.port = 18888
node.http.fullNodePort = 8090
node.rpc.port = 50051

seed.node {
  ip.list = ["3.225.171.164:18888", "52.53.189.99:18888"]
}

vm.supportConstant = true
node.metrics { prometheus { enable = true, port = 9527 } }
```
