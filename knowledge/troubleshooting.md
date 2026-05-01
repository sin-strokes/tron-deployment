# Troubleshooting java-tron Nodes

## Sync Lag

The node falls behind the current block height.

**Symptoms**: Block height delta grows over time; API returns stale data.

**Causes and fixes**:
- **Insufficient CPU/memory**: Check system load with `top`/`htop`. Upgrade if CPU is consistently above 80% or memory is swapping.
- **Slow disk I/O**: LevelDB/RocksDB performance degrades on HDD. Use SSD (NVMe preferred). Check with `iostat -x 1`.
- **Too few peers**: See "Peer Starvation" below.
- **JVM garbage collection pauses**: Check GC logs. Tune heap size and GC algorithm (see best-practices.md).
- **Network bandwidth**: Syncing requires sustained throughput. Verify with `iftop` or `nload`.

**Diagnostic steps**:
1. Compare local block height (`/wallet/getnowblock`) with a known public node
2. Check peer count (`/wallet/listnodes`)
3. Review `logs/tron.log` for WARN/ERROR entries
4. Monitor CPU, memory, disk I/O, and network

## Peer Starvation

The node has zero or very few connected peers.

**Symptoms**: Sync stalls; `listnodes` returns empty or very short list.

**Causes and fixes**:
- **Firewall blocking P2P port**: Ensure TCP 18888 (or custom `node.listen.port`) is open inbound and outbound.
- **Wrong `seed.node` list**: Verify seed nodes match the target network (mainnet vs testnet).
- **Wrong `node.p2p.version`**: Must match the network. Mainnet and testnet use different versions.
- **NAT/cloud security group**: Ensure the node's public IP is reachable on the P2P port.
- **`node.active` misconfigured**: If set, the node only connects to listed peers. Remove or correct.

**Diagnostic steps**:
1. Check `netstat -tlnp | grep 18888` to confirm the port is listening
2. Test inbound connectivity from an external host
3. Verify seed node addresses resolve and are reachable

## Disk Full

The node stops syncing or crashes when disk space is exhausted.

**Symptoms**: Node crashes with I/O errors; LevelDB/RocksDB corruption possible.

**Causes and fixes**:
- **Unpruned state growth**: Mainnet state grows ~200 GB/year. Plan capacity accordingly.
- **Log accumulation**: Enable log rotation (see best-practices.md).
- **Snapshot/backup files left on disk**: Clean up old snapshots.

**Diagnostic steps**:
1. Run `df -h` to check available space
2. Identify largest directories: `du -sh /path/to/output-directory/*`
3. Check log directory size

**Recovery**:
- Free space immediately (remove old logs, snapshots)
- If database is corrupted, re-sync from a recent snapshot
- Expand disk or migrate to larger volume

## Port Conflicts

Another process occupies a port java-tron needs.

**Symptoms**: Node fails to start; logs show "Address already in use".

**Diagnostic steps**:
1. `lsof -i :8090` (or the conflicting port)
2. Stop the conflicting process or change the port in config

**Common conflicts**:
- Multiple java-tron instances on the same host
- Other HTTP servers on 8090
- gRPC services on 50051

## Out of Memory (OOM) Kill

The OS kills the java-tron process due to memory exhaustion.

**Symptoms**: Process disappears; `dmesg | grep -i oom` shows kill entry.

**Causes and fixes**:
- **JVM heap too large for available RAM**: Leave 4-8 GB for OS and page cache. On a 32 GB host, set `-Xmx24g`.
- **JVM heap too small**: Frequent GC and eventual OOM. Monitor with `-XX:+PrintGCDetails`.
- **Memory leak (rare)**: Check java-tron GitHub issues for known bugs in your version.

**Diagnostic steps**:
1. Check `dmesg` or `/var/log/kern.log` for OOM messages
2. Review JVM flags in the start script
3. Monitor RSS with `ps aux | grep java`

## JVM Crash

The JVM itself crashes (segfault, internal error).

**Symptoms**: `hs_err_pid*.log` file created in the working directory.

**Causes and fixes**:
- **Incompatible JDK version**: recent java-tron releases (4.7+) target JDK 17 (OpenJDK). The official tronprotocol/java-tron container image bundles JDK 17. Older 4.x lines may still expect JDK 8 — match the image.
- **Native memory exhaustion**: Check ulimits (`ulimit -a`), especially `max memory size` and `open files`.
- **Hardware fault**: Run `memtest86` if crashes are random.

**Diagnostic steps**:
1. Read the `hs_err_pid*.log` file for the crash reason
2. Verify JDK version: `java -version`
3. Check ulimits and system resource limits

## Config Errors

The node fails to start due to HOCON parse errors or invalid values.

**Symptoms**: Immediate exit with config-related exception in logs.

**Common mistakes**:
- Missing closing braces or brackets in HOCON
- Using `=` instead of `:` (both are valid in HOCON, but nested structures need braces)
- Invalid IP addresses in `seed.node` list
- Wrong `net.type` value (must be `mainnet` for mainnet)
- Witness private key formatting errors

**Diagnostic steps**:
1. Validate config syntax before starting: `trond config validate <intent.yaml>`
2. Check the first ERROR line in logs -- it usually points to the exact config issue
3. Compare against a known-good config from `templates/`
