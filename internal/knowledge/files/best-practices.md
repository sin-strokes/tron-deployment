# Production Best Practices

## Pre-Deployment Checklist

- [ ] SSD storage provisioned with sufficient IOPS
- [ ] JDK 8 installed and verified
- [ ] Firewall rules configured (see below)
- [ ] NTP time synchronization enabled
- [ ] Log rotation configured
- [ ] Monitoring agent installed
- [ ] Backup strategy defined

## JVM Tuning

### Memory Sizing

| Host RAM | Recommended -Xmx | -Xms | Notes |
|---|---|---|---|
| 16 GB | 10g | 10g | Lite FullNode only |
| 32 GB | 24g | 24g | Standard FullNode |
| 48 GB | 36g | 36g | Recommended FullNode |
| 64 GB | 48g | 48g | Witness / high-traffic API |

Always set `-Xms` equal to `-Xmx` to avoid heap resizing overhead.

### GC Configuration

For java-tron on JDK 8, use CMS or G1:

**G1 (recommended for heaps above 16 GB)**:
```
-XX:+UseG1GC
-XX:G1HeapRegionSize=16m
-XX:MaxGCPauseMillis=200
-XX:InitiatingHeapOccupancyPercent=45
```

**CMS (alternative)**:
```
-XX:+UseConcMarkSweepGC
-XX:+CMSParallelRemarkEnabled
-XX:+UseCMSInitiatingOccupancyOnly
-XX:CMSInitiatingOccupancyFraction=70
```

### Additional JVM Flags

```
-XX:+HeapDumpOnOutOfMemoryError
-XX:HeapDumpPath=/var/log/tron/heapdump.hprof
-XX:+PrintGCDetails
-XX:+PrintGCDateStamps
-Xloggc:/var/log/tron/gc.log
-XX:+UseGCLogFileRotation
-XX:NumberOfGCLogFiles=5
-XX:GCLogFileSize=50m
```

## Monitoring

### Prometheus Metrics

java-tron exposes metrics on port 9527 by default. Enable in config:

```
node.metrics {
  prometheus {
    enable = true
    port = 9527
  }
}
```

Key metrics to watch:
- `tron_block_height` -- current block number
- `tron_peer_count` -- connected peers
- `tron_transaction_count` -- transaction throughput
- JVM heap usage, GC pause times

### Alerting Thresholds

- Block height delta > 50 blocks behind public reference node
- Peer count < 5
- Disk usage > 80%
- JVM heap usage > 90%
- GC pause > 2 seconds

## Log Rotation

java-tron uses logback. Configure in `logback.xml`:

- Set max log file size to 100 MB
- Keep 30 days of history
- Total cap at 10 GB

If using systemd, also configure journald log limits in `/etc/systemd/journald.conf`.

For Docker deployments, set `--log-opt max-size=100m --log-opt max-file=10`.

## Backup Strategy

### Database Snapshots

- Use official TRON snapshot service for initial sync (faster than syncing from genesis)
- Schedule periodic snapshots of the `output-directory` for disaster recovery
- Stop the node before taking a filesystem snapshot to avoid corruption
- Consider LVM snapshots or cloud disk snapshots for minimal downtime

### Config Backup

- Store intent files and configs in version control
- Back up witness private keys securely (encrypted, offsite)
- Document JVM flags and environment variables

## Security Hardening

### Network

- Run the node as a non-root user
- Restrict SSH access to management IPs only
- Use key-based SSH authentication (disable password auth)
- Keep the OS and JDK patched

### Firewall Rules

| Port | Direction | Source | Purpose |
|---|---|---|---|
| 18888 | Inbound | 0.0.0.0/0 | P2P (required) |
| 8090 | Inbound | Trusted IPs | HTTP API |
| 8091 | Inbound | Trusted IPs | Solidity HTTP API |
| 50051 | Inbound | Trusted IPs | gRPC |
| 50061 | Inbound | Trusted IPs | Solidity gRPC |
| 9527 | Inbound | Monitoring IPs | Prometheus metrics |

- Never expose API ports (8090, 50051) to the public internet without authentication or a reverse proxy.
- For witness nodes, restrict API access as tightly as possible.

### Witness Key Protection

- Use the keystore mechanism instead of plaintext `localwitness` when possible
- Restrict file permissions on any file containing the private key (`chmod 600`)
- Consider hardware security modules (HSM) for high-value witnesses

## Upgrades

- Test new java-tron versions on a non-production node first
- Review the changelog for config changes or database migrations
- Plan for downtime during major version upgrades
- Keep the previous version binary available for rollback
