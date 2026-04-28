# Chain Database Snapshots

A fresh TRON fullnode normally syncs from the genesis block. On mainnet
that takes days and many GB of P2P traffic. The official mirrors publish
periodic dumps of the chain database; trond fetches and extracts them so
a node can start *caught-up*.

## When to use a snapshot

- First-time deployment of a new fullnode.
- Re-provisioning after data corruption or disk loss.
- Standing up a CI fixture that needs a "live-ish" chain state without
  burning compute on a multi-day sync.

A snapshot is **not** what you want when:
- You're running a witness with already-mined blocks (snapshot doesn't
  ship `userdata/`; trond preserves it across extraction, but the rest
  of the database will overwrite anything custom under `database/`).
- You're on a private network — snapshots only exist for mainnet and
  Nile.

## Streaming pipeline

`snapshot download` does *not* persist the upstream `.tgz`. The HTTP body
flows through one io.Reader chain:

```
HTTP body
  → TeeReader → md5.Hash       (verify integrity inline)
  → progress wrapper           (10 Hz UI updates, no buffering)
  → gzip.Reader                (streaming decompress)
  → tar.Reader                 (entry-by-entry)
  → os.OpenFile / io.Copy      (each tar entry written directly to disk)
```

So peak disk usage is roughly *the extracted size* — you don't need 2×
to hold the tarball as a temporary staging file. Wall-clock time is
roughly the network transfer time (CPU is far cheaper than the wire).

## Lite vs. full

| Kind | Size (approx) | Use case |
|---|---|---|
| `lite` | ~50 GB mainnet, ~30 GB nile | Default. Holds recent blocks; fine for fullnode operation |
| `full` | ~2 TB mainnet | Archive node, indexer, anything that needs full history |

LevelDB vs. RocksDB: java-tron defaults to LevelDB. One mainnet mirror
publishes a RocksDB encoding (`--db-engine rocksdb`); only choose it if
you've explicitly configured `storage.db.engine=ROCKSDB` via
`config_overrides`.

## Picking a mirror

```bash
trond snapshot sources              # the full table
```

Defaults are deliberate:

- `--network mainnet` (no other flags): Singapore lite mirror — fastest
  setup for the common case.
- `--network nile`: the only nile mirror (S3 https endpoint).

Override with `--region america`, `--type full`, or pin a specific host
via `--domain 35.247.128.170`.

## Disk-space pre-check

Before any GET, trond:

1. HEADs the tarball URL → reads `Content-Length`.
2. `Statfs(destination)` → reads available bytes.
3. Refuses to start if free space < `Content-Length × 2`.

The 2× headroom covers concurrent extraction (when the new database is
landing while the old one hasn't been removed yet) and the slop java-tron
adds when it first opens the new DB.

## Existing-database handling

Two adjacent directories under the destination get special treatment:

| Path | Behaviour |
|---|---|
| `output-directory/database/` | If non-empty, refuse without `--force` (HUMAN_REQUIRED, exit 10). With `--force`, files are overwritten in place. |
| `userdata/` | Always preserved. Holds witness keys / operator state and is **not** part of the snapshot tarball. |

Pre-existing symlinks at any target path are refused, never followed.
Any tar entry containing `..` is rejected before `open()`.

## MD5 verification

Mainnet mirrors publish `<tarball>.tgz.md5sum` sidecars. trond:

1. HEADs the sidecar in preflight (records "has md5 sidecar: true/false").
2. Downloads the sidecar (a few hundred bytes).
3. Hashes the tarball stream while extracting.
4. Compares — mismatch fails the operation with the database in whatever
   partial state extraction reached.

Nile and the occasional outage may leave the sidecar absent. trond will
still extract; the result message reads `(md5 sidecar absent — not
verified)`. Pass `--no-verify` to suppress that note when you've made
the choice deliberately.

## Long downloads: `--detach`

Mainnet full snapshots can take many hours over a residential link. The
foreground form ties the download to the controlling terminal — closing
the SSH session or laptop lid sends SIGHUP and the work is lost.

`--detach` re-execs trond with `SysProcAttr.Setsid=true`. The child:

- Becomes a session leader (immune to SIGHUP from the parent's terminal).
- Has its stdin tied to `/dev/null`, stdout+stderr to
  `~/.trond/snapshots/<id>.log`.
- Is reparented to PID 1 (launchd / init) once the parent calls
  `Process.Release()`.

The parent prints the job id and exits. Manage the job:

```bash
trond snapshot jobs                       # ID, PID, running/stopped, last log line
trond snapshot logs <job-id> -f           # follow progress
trond snapshot stop <job-id>              # SIGTERM
trond snapshot stop <job-id> --force      # SIGKILL (last resort)
```

The job manifest lives at `~/.trond/snapshots/<id>.json`. Liveness uses
`kill(0)` so finished jobs persist as `state=stopped` with the last log
line as `exit_note`. Delete a finished job's files manually if you don't
want it shown in `jobs` output.

## Putting a snapshot under a managed node

For docker-runtime nodes, the chain DB lives in a docker volume — not a
host path you can extract into. The clean pattern is:

1. Add a `storage.path: /srv/tron/<node>` to your intent.
2. Run `trond snapshot download --network mainnet --to /srv/tron/<node>`.
3. `trond apply --intent <intent.yaml>` — the bind-mount picks up the
   pre-extracted database on first start.

For jar-runtime nodes, `trond snapshot download --node <name>` resolves
the destination from `install_path` in state automatically.

## Common errors

`HUMAN_REQUIRED: existing database at <path>; pass --force to overwrite`
  - Means the destination already has data. Either you want to keep it
    (delete the snapshot intent) or `--force`.

`DISK_SPACE_ERROR: need ~X GB free in <path>, have Y GB`
  - Free space, then retry. There's no `--ignore-space` escape hatch by
    design; running out mid-extract leaves a broken half-database.

`md5 mismatch: expected ..., got ...`
  - The tarball got corrupted in flight or the mirror is serving a
    different file than its sidecar advertises. Retry; if the mismatch
    persists, switch `--region` or `--domain`.
