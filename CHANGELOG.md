# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `trond doctor` — environment self-check (state integrity, lock health, version drift)
- `trond completion --install` — convenience installer for shell completions
- `config render --node N` — render only one node from a multi-node intent
- `--overlay <path>` — second intent file merged on top of the primary one
- README: "Repository Evolution" section explaining the move from
  hand-edited templates to declarative CLI

### Changed
- `goreleaser` now produces homebrew tap, .deb, .rpm, and a docker image in
  addition to the existing tar.gz archives
- CI matrix expanded: `lint`, `test+coverage`, and `govulncheck` jobs run on
  every PR alongside the existing e2e suite

### Fixed
- (none yet)

## [0.1.0-alpha] — 2026-XX-XX

Initial public alpha. The project transitions from a curated set of HOCON
configuration templates into a CLI for declarative TRON node deployment.

### Added
- 32 CLI commands across lifecycle (apply / stop / start / restart / upgrade /
  rollback / remove), configuration (validate / render / diff / docs),
  observability (status / list / logs / health / diagnose / verify / inspect /
  events), test-harness SDK (exec / files / wait), chaos primitives
  (disconnect / connect / partition / heal), private networks (create / add /
  status / destroy), environment (preflight / bootstrap), knowledge base, and
  meta (version / completion / help)
- Declarative intent.yaml schema covering ~50 fields:
  target (local/ssh, runtime, auto_ports), node (type, version, image, ports,
  resources, jvm, storage, restart, extra_env, extra_args, labels, networks,
  depends_on, healthcheck, ulimits, extra_hosts, entrypoint, logging,
  shm_size, jar source URL+SHA256), network_overrides
  (seeds, active_peers, p2p_version, discovery, max_connections, …),
  witness_key (private_key_env, keystore_path, account_address),
  config_overrides (arbitrary HOCON dotted-key escape hatch)
- HOCON two-pass render: in-place key rewrites + appended override block
- Compose render aligned with the official `tronprotocol/java-tron` image:
  `/java-tron/conf`, `/java-tron/output-directory`, `/java-tron/logs`
- SSH host-key verification with explicit MITM detection (TOFU opt-in via
  `TROND_SSH_ACCEPT_NEW_HOSTS=1`)
- SSH command whitelist enforced at `Exec` entry
- Private key never written to env or stdout — `PrivateKey` type redacted in
  every formatter; witness key inlined into HOCON (which is 0600 on disk)
- `--state-dir` / `TROND_STATE_DIR` for parallel test enclaves
- `target.auto_ports: true` allocates free TCP+UDP ports automatically
- `network create` auto-wires `node.active` peering between siblings
- Audit log (JSONL) for every mutating command, streamable via `events --follow`

### Repository changes
- HOCON templates remain at the repository root (`main_net_config.conf`,
  `test_net_config.conf`, `private_net_config.conf`) and continue to track
  upstream. `make sync-templates` refreshes them
- Embedded copies under `internal/render/templates/` are kept in sync at
  release time and bundled into the binary so `trond config render` works
  from any working directory

[Unreleased]: https://github.com/tronprotocol/tron-deployment/compare/v0.1.0-alpha...HEAD
[0.1.0-alpha]: https://github.com/tronprotocol/tron-deployment/releases/tag/v0.1.0-alpha
