# Research: trond CLI Platform

**Feature**: 001-trond-cli-platform
**Date**: 2026-04-08

## R1: Go HOCON Parsing

**Decision**: `github.com/gurkankaymak/hocon`
**Rationale**: Most actively maintained Go HOCON library. Supports includes, substitutions,
multi-line strings — all used by java-tron configs.
**Alternatives rejected**:
- Regex patching: fragile on nested structures
- Java subprocess parser: requires JDK, defeats static binary
- HOCON→JSON conversion: lossy (comments, includes)
**Risk**: Edge-case incompatibilities. Mitigated by config render validation test suite.

## R2: SSH Execution

**Decision**: `golang.org/x/crypto/ssh` direct sessions + SFTP
**Rationale**: No dependency on host ssh binary; programmatic key loading; structured errors.
**File transfer**: SFTP subsystem over same connection.

## R3: JVM Tuning

**Decision**: Port `java-tron/start.sh` (lines ~180-280) to `internal/render/jvm.go`
**Key logic**: memory-based heap sizing, G1/CMS selection by JDK version, GC log config.
**Tested via**: Unit tests with known memory inputs → expected JVM flags output.

## R4: State File

**Decision**: `~/.trond/state.json` — single JSON file, OS flock for locking
**Format**: Array of ManagedNode records (name, hashes, version, target, status, timestamp)
**Rejected**: SQLite (CGO), per-node files (atomic listing), remote backends (V2)

## R5: Docker Compose Execution

**Decision**: `os/exec` to `docker compose` CLI (Compose V2)
**Rationale**: Matches tron-docker trond pattern; compose files are inspectable artifacts.

## R6: Release Signing

**Decision**: GoReleaser + cosign + slsa-github-generator
**Output**: Cross-compiled binaries + SHA256 + cosign signatures + SLSA L3 provenance

All NEEDS CLARIFICATION items resolved. No outstanding unknowns.
