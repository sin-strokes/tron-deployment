# AGENTS.md — for AI agents that call `trond`

This file is **for agents that USE trond as a tool** (Claude, ChatGPT,
Cursor, Cline, autonomous workflows, CI bots, etc.). If you are an
agent EDITING this repository, read `CLAUDE.md` instead — that one
covers code style and architecture rules for changes to trond itself.

The contract here is meant to be self-contained: an agent that reads
this file once should be able to deploy, diagnose, and recover TRON
nodes without trial-and-error grepping through `--help` output.

---

## When to use trond

Use trond when the task involves any of:

- Deploying a TRON fullnode / witness / solidity / lite node (mainnet,
  Nile testnet, or private network), local Docker or remote SSH.
- Inspecting node state: sync progress, peer count, block height,
  endpoints, logs.
- Diagnosing a node that's misbehaving (the user says "my node isn't
  syncing" / "TPS is bad" / "it crashed").
- Bringing up a multi-node private network for testing.
- Pulling an official chain-database snapshot (mainnet sync from genesis
  takes days; snapshot drops it to hours).
- Verifying release artifacts (cosign signature, sha256 checksum).

Do **not** use trond for:

- Smart contract deployment, transaction signing, wallet operations —
  that's other tooling (`tronbox`, `tron-cli`, etc.).
- Editing java-tron source code or building it from scratch — trond
  consumes the official `tronprotocol/java-tron` Docker image / jar.
- Writing the intent file from a totally blank prompt — first ask what
  network / node type / target the user wants. trond requires a
  declarative `intent.yaml`; guessing is more harmful than asking.

---

## Universal calling convention

Always pass `-o json` (or `--output json`) and parse the structured
response. Text mode is for humans; agents should never grep stdout.

Always set `TROND_STATE_DIR` for concurrent / multi-agent scenarios so
sessions don't collide on `~/.trond/state.json`:

```bash
export TROND_STATE_DIR=/tmp/trond-$AGENT_SESSION_ID
trond <command> -o json
```

Set `auto_ports: true` under `target:` in intent files when the host
might already be running TRON nodes — trond allocates free OS ports
and persists them so the agent doesn't have to negotiate ports.

---

## Exit codes — what each means and how to react

| Code | Meaning | Recommended retry strategy |
|---|---|---|
| 0 | success | continue to next step |
| 1 | general error (`WAIT_TIMEOUT`, `EXEC_ERROR`, `NODE_NOT_FOUND`, etc.) | inspect `error_code` in the JSON, follow `suggestions[]`; if transient (network), retry once with exponential backoff |
| 2 | validation error in intent.yaml | DO NOT retry — fix the intent file, then re-run |
| 3 | target unreachable (SSH/Docker connection) | one retry after 5s; if still failing, surface to user with the host/connection detail |
| 4 | preflight failure (missing dependency on target) | DO NOT retry — present `missing_components` array to user; offer `trond bootstrap` |
| 10 | `HUMAN_REQUIRED` — destructive op needs explicit confirmation | DO NOT silently retry with `--auto-approve` / `--confirm` unless the user has authorised that for this session |

Every error JSON has the same shape:

```json
{
  "error_code": "VALIDATION_ERROR",
  "exit_code": 2,
  "message": "intent.yaml line 12: target.type must be one of [local ssh]",
  "suggestions": [
    "Set target.type to 'local' for local Docker deploys",
    "Or 'ssh' for remote deploys; also set target.host and target.user"
  ]
}
```

Process `suggestions[]` in order. The first entry is the most
prescriptive and usually fixes the issue automatically; later entries
are alternative fixes for less-likely causes.

---

## Workflow 1 — Deploy a node from scratch

The canonical chain. Each step's output feeds the next; bail on any
non-zero exit unless explicitly told to continue.

```bash
# 1. Validate the intent shape — catches typos before any network IO.
trond config validate intent.yaml -o json
# Output: {"valid": true, "name": "my-fullnode", "network": "mainnet", "node_count": 1}
# Bail on exit 2.

# 2. Preflight the target — missing docker, low memory, port conflicts.
trond preflight --intent intent.yaml -o json
# Output: {"target": "local", "checks": [...], "overall": "pass" | "warning" | "fail"}
# Bail on exit 4. On "warning" surface details to user but proceed.

# 3. Plan — show what apply WOULD do without executing.
trond plan --intent intent.yaml -o json
# Output: {"name": "...", "current_state": "not_deployed" | "running" | ...,
#          "changes": [{"type":"create"|"update"|"delete", "field":"...", ...}],
#          "destructive": false, "estimated_downtime_seconds": 0}
# If changes is empty AND current_state is "running", you can skip apply.

# 4. Apply — idempotent. Re-running the same intent is a no-op.
trond apply --intent intent.yaml --auto-approve --wait -o json
# Output: {"name":"...", "result":"created"|"updated"|"no_change",
#          "endpoints":{"http":"http://127.0.0.1:8090","grpc":"127.0.0.1:50051"},
#          "duration_ms": 3500, "ready": true}
# --wait blocks until the HTTP API responds. Without --wait, the
# container is started but may not yet be syncing.
# On exit 10 (HUMAN_REQUIRED): surface the diff to the user, ask for
# approval, then retry with --auto-approve. NEVER silently re-run.

# 5. Verify — post-deploy health gate. Polls until block_height > 0.
trond verify --intent intent.yaml --timeout 5m -o json
# Output: {"name":"...", "verified": true, "block_height": 12345678,
#          "attempts": 3}

# 6. Status — readable summary the agent shows the user.
trond status my-fullnode -o json
# Output: {"name":"...", "status":"running", "block_height": ...,
#          "peer_count": 12, "is_synced": true, "api_endpoints":{...}}
```

**Idempotency**: `apply` is hash-gated. Same intent → no-op. Changed
intent without `--auto-approve` → exit 10. The agent should always
diff first via `plan` and only pass `--auto-approve` when the user
approved the changes shown by `plan`.

---

## Workflow 2 — Diagnose a misbehaving node

When the user complains "my node is broken / not syncing / slow",
collect structured facts before suggesting fixes.

```bash
# 1. Check if the process is even alive at the runtime layer.
trond status <node-name> -o json
# Look at: status (running/stopped/error), is_synced, lag (block delta),
# peer_count. If status != "running", go to logs+health straight away.

# 2. Run the structured health/diagnose suite.
trond diagnose <node-name> -o json
# Output: {"checks": [
#   {"name":"sync_progress", "status":"pass"|"warning"|"fail", "message":"...", "suggestions":[...]},
#   {"name":"peer_count", ...},
#   {"name":"disk_space", ...},
#   {"name":"port_listening", ...},
#   ...
# ], "overall":"..."}
# Each check carries its own suggestions[]. Process failed checks first.

# 3. If diagnose isn't conclusive, look at logs.
trond logs <node-name> --tail 200 -o json
# Output: {"lines":[{"ts":"...","level":"WARN","message":"..."}]}
# Pattern-match common signatures: "Multiple garbage collectors selected",
# "private key must be 64 hex string", "Connection refused", "out of memory".

# 4. If a remediation exists, propose it. Don't silently apply changes.
#    Common remediations:
#    - peer_count=0 → check seed nodes / network_overrides.seeds
#    - is_synced=false but lag is shrinking → wait, not broken
#    - is_synced=false and lag growing → restart, then diagnose again
#    - disk_space.fail → user needs to free space; trond can't fix this
```

The `events` audit log is also worth pulling for context:

```bash
trond events --since 1h -o json
# JSONL stream of past commands: who ran what, when, what changed.
```

---

## Workflow 3 — Skip genesis sync via snapshot

A fresh mainnet fullnode otherwise spends days catching up. Snapshots
drop it to hours.

```bash
# 1. Show available mirrors so the user (or you) can pick.
trond snapshot sources -o json
# Output: {"sources":[{"network":"mainnet","kind":"lite","region":"singapore",
#                       "domain":"34.143.247.77","approx_size_gb":46, ...}, ...]}

# 2. Dry-run to confirm disk space + URL + would-overwrite check.
trond snapshot download --network mainnet --to /srv/tron/<node> --dry-run -o json
# Output: {"preflight":{"expected_size_bytes":...,"free_bytes":...,
#          "needed_bytes":...,"would_overwrite":false,"has_md5_sidecar":true}, ...}
# If needed_bytes > free_bytes → tell user, abort.
# If would_overwrite → ask user before passing --force.

# 3. Start the actual download in background.
trond snapshot download --network mainnet --to /srv/tron/<node> --detach -o json
# Output: {"job_id":"20260501-103300-abc1","pid":12345,"log_path":"...",
#          "dest":"/srv/tron/<node>","backup":"backup20260501","kind":"lite"}

# 4. Poll until done. The download can take hours for mainnet full.
while true; do
  STATUS=$(trond snapshot jobs -o json | jq -r ".jobs[] | select(.id==\"$JOB_ID\")")
  RUNNING=$(echo "$STATUS" | jq -r .running)
  if [ "$RUNNING" = "false" ]; then break; fi
  sleep 60
done
# Or stream progress:  trond snapshot logs <job_id> -f

# 5. Once done, the chain DB sits at <dest>/output-directory/database.
#    Now apply with an intent whose storage.data points there.
trond apply --intent intent-with-snapshot.yaml --auto-approve --wait -o json
```

When the download finishes, its manifest + log stay under
`~/.trond/snapshots/<id>.{json,log}` so you can audit later. Stale
ones (default: stopped + older than 7 days) can be reclaimed with:

```bash
trond snapshot prune --dry-run            # preview
trond snapshot prune                      # default policy
trond snapshot prune --all -o json        # ignore age, return JSON
```

The intent needs `storage.data: /srv/tron/<node>/output-directory` so
the bind mount lines up with where the tarball extracts. See
`examples/mainnet-fullnode-snapshot.yaml`.

**Why the two-stage flow**: `apply` is supposed to be seconds-fast and
idempotent. A multi-hour download inside apply would break the contract
for every CI / orchestration caller. Keep them separate.

---

## Workflow 4 — Multi-node private network

For testing, demos, or development against a private chain.

```bash
# 1. Use the example as a starting point.
cp examples/private-network.yaml my-net.yaml
# Edit witness count / fullnode count / per-node memory.

# 2. Validate.
trond config validate my-net.yaml -o json

# 3. Preflight (each node target gets checked).
trond preflight --intent my-net.yaml -o json

# 4. Create the whole network in one shot. trond auto-wires
#    node.active between siblings so peering works under auto_ports.
SR_KEY=da146374a75310b9666e834ee4ad0866d6f4035967bfc76217c5a495fff9f0d0 \
  trond network create --intent my-net.yaml --wait -o json
# Output: {"name":"pn", "nodes":[{"name":"pn-witness", "endpoints":{...}}, ...],
#          "ready_count": 2}

# 5. Inspect / probe / chaos-test.
trond network status pn -o json
trond inspect --network pn -o json
trond exec pn-witness -- /java-tron/bin/FullNode --help

# 6. Cleanup. --confirm must match the network name (refuses typos).
trond network destroy pn --confirm pn -o json
```

---

## Test-harness primitives (for agents driving CI / fuzz / chaos)

These exist for agents that drive trond programmatically rather than
managing a single user's nodes.

```bash
# Block until a probe succeeds (port listen, HTTP endpoint, exec output).
trond wait <node> --port 8090 --timeout 60s -o json
trond wait <node> --http "http://127.0.0.1:8090/wallet/getnowblock" \
  --json '.block_header.raw_data.number > 100' --timeout 5m

# Exec arbitrary commands inside a node (docker exec / SSH exec).
trond exec <node> -- ls /java-tron/output-directory

# Push/pull files. Use for keystores, custom configs, snapshots.
trond files put <node> ./local.conf /java-tron/conf/custom.conf
trond files get <node> /java-tron/logs/tron.log ./tron.log

# Chaos primitives — works at the docker network layer.
trond disconnect <a> <b>          # tear down peer link
trond connect    <a> <b>          # restore
trond partition  --groups 'a,b|c,d'  # split network into partitions
trond heal                        # restore everything

# Audit feed. Useful when an agent wants to see what it did earlier.
trond events --since 1h --follow -o json
```

---

## Concurrency and isolation

Each agent session should pin its own state-dir:

```bash
TROND_STATE_DIR=/tmp/trond-${SESSION_ID} trond <command>
# state.json, audit.log, deployments/, snapshots/ all rooted here
```

This is the ONLY supported way to run two agent sessions on the same
host without corrupting state. The default `~/.trond/` is single-user.

`auto_ports: true` in the intent's target block solves port collisions
when multiple sessions deploy on the same host.

---

## Knowledge / reference inside trond

trond ships embedded markdown topics — fetch by name when you need
deeper context than this file:

```bash
trond knowledge                       # list topics
trond knowledge <topic> -o json       # one topic
```

Topics include: `node-types`, `troubleshooting`, `best-practices`,
`config-reference`, `cloud-deployment`, `test-harness`, `snapshots`,
`release-signatures`. Read these when the user's question maps to a
topic; don't paraphrase from training data.

---

## Anti-patterns — things to NEVER do

1. **Don't silently retry HUMAN_REQUIRED with `--auto-approve`**. Exit
   10 means a destructive change requires user consent. Show the diff,
   ask, only then re-run. Hard rule, no exceptions.

2. **Don't skip preflight on remote SSH targets**. Missing Docker / JDK
   / disk space on the remote will turn a 3-second `apply` into a
   confusing failure mid-deploy. Run preflight first, surface failures.

3. **Don't grep `trond --help` to discover commands**. Use
   `trond knowledge` for tutorials, `trond config docs <key>` for HOCON
   keys, and `trond schema` for the full machine contract.

4. **Don't write witness keys into `extra_env`**. Use the
   `witness_key.private_key_env` field; trond inlines the value into
   the rendered HOCON at apply time. The container env path silently
   fails because typesafe-config doesn't substitute `${VAR}`.

5. **Don't bypass `auto_ports` and try to negotiate ports yourself**.
   trond does TCP+UDP free-port detection that handles the P2P
   discovery UDP requirement; rolling your own usually breaks under
   docker compose's port binding semantics.

6. **Don't use `--detach` for short downloads**. The job machinery has
   overhead. Foreground is fine for nile lite (~30 GB on a fast
   connection).

7. **Don't trust unsigned releases**. The release pipeline produces
   `checksums.txt` + `.sig` + `.pem` for every tag. Validation recipe
   is in `trond knowledge release-signatures` or README.

8. **Don't blow away `~/.trond/snapshots/<id>.json`** to clean up jobs.
   Use the upcoming `snapshot prune` (or just `rm` the JSON+log pair
   when the job is `state=stopped`).

---

## Schema discovery

`trond schema [-o json]` dumps the full command tree + flag types +
output JSON Schemas as one machine-readable manifest. Pin against the
top-level `schema_version` field for stability.

```bash
# Full manifest (every command, every flag, every documented schema)
trond schema -o json | jq '.schema_version, (.commands | length)'

# One command's spec
trond schema apply -o json
trond schema "snapshot download" -o json

# Just the output JSON Schema for one command (useful for agent input
# validation when piping trond's output through `ajv` or similar)
trond schema apply --output-only -o json
```

## MCP server

For chat-based / IDE-embedded agents that can't shell out (Claude
Desktop, Cursor, Cline, Continue.dev, Zed AI, ChatGPT Apps), trond
runs as a Model Context Protocol server:

```bash
trond mcp        # speaks JSON-RPC over stdio
```

Configure once in your client. Example for Claude Desktop
(`~/.config/claude-desktop/config.json` or
`%APPDATA%\Claude\config.json`):

```json
{
  "mcpServers": {
    "trond": { "command": "/usr/local/bin/trond", "args": ["mcp"] }
  }
}
```

The server registers 16 tools (read-only unless marked):

- **inspection** (3): `list`, `status`, `inspect`
- **diagnostic** (4): `doctor`, `version`, `health`, `diagnose`
- **config** (2): `config_validate`, `config_render`
- **lifecycle** (2): `plan`, `apply` (destructive)
- **snapshot** (4): `snapshot_sources`, `snapshot_list`, `snapshot_jobs`,
  `snapshot_download` (destructive, emits MCP progress notifications)
- **knowledge** (2): `knowledge_list`, `knowledge_get`

Destructive tools carry the MCP `destructiveHint` annotation so MCP
clients prompt the user. The server's `Instructions` field
auto-injects guidance about the canonical workflows so the LLM picks
up AGENTS.md context without the user having to paste it.

## Recipes — pre-built multi-step playbooks

Five canonical multi-step workflows from the sections above are
codified as declarative YAML in `recipes/*.yaml` and run via:

```bash
trond recipe list                                   # everything available
trond recipe show <name>                            # YAML + params + steps
trond recipe run <name> --param key=value [...]     # execute end-to-end
trond recipe run <name> ... --dry-run               # print resolved chain, no exec
trond recipe run <name> ... --resume-from <step>    # skip ahead after a partial run
```

The runner re-execs the trond binary itself for each step (no shell
dependency beyond `exec`), captures stdout JSON, and feeds named
fields forward via `{{ steps.<id>.<field> }}` substitution. Steps
declare `on_failure: abort | continue | rollback`; rollback steps
run as a best-effort cleanup pass when triggered.

The output of `recipe list / show / run` is documented as JSON Schema
(`schemas/output/recipe-{list,show,run}.schema.json`); pull via
`trond schema "recipe run" --output-only -o json` or validate parsed
output against the embedded schema.

Shipped recipes:

| Name | What it does |
|---|---|
| `nile-test-fullnode` | validate → preflight → apply --wait → verify (4 steps; smoke-test workflow) |
| `fresh-mainnet-fullnode-with-snapshot` | validate → preflight → snapshot download → apply --wait → verify, with rollback to stop the failed node (5 steps) |
| `upgrade-with-verify` | snapshot status → upgrade → verify, with auto-rollback on verify failure (3 steps + 1 rollback) |
| `recover-failed-upgrade` | diagnose → rollback → status, for post-upgrade triage (3 steps) |
| `destroy-private-network-cleanly` | network status → network destroy with confirm gate (2 steps) |

For an MCP-driven agent, "run a recipe" is a single tool call that
encapsulates 3–5 underlying tool calls and the failure / rollback
state machine. For a CI pipeline, it replaces a 30-line bash script
with one declarative file. For a human at a terminal, it documents
the canonical workflow as runnable code.

---

## Versioning of this contract

Every JSON output that carries a `schema_version` field follows
semantic versioning at the field level: existing fields are stable
within a major version; additions are minor; renames or removals are
major. This file is updated alongside any contract change. If you
write an agent against trond X.Y, pin to that version and re-test on
upgrade.

If a JSON output's shape doesn't match what's documented here, that's
a bug — file an issue with the actual output and the trond version
(`trond version -o json`).
