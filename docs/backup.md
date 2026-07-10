# Backup & Restore

Agent Task Editor stores everything you can't regenerate — tasks, run
history, per-run cost data, label-history audit trails, agent notes, and
diff review comments — in a single SQLite database file. This page covers
how to back it up safely, how to restore it, and options for continuous
offsite replication.

**Self-hosted implies you own your data.** A naive `cp`/`docker cp` of a
live SQLite file under write load can produce a corrupt copy (SQLite's
WAL mode means the on-disk `.db` file alone isn't a consistent snapshot
without also copying the `-wal`/`-shm` sidecar files, and even then it's
racy under concurrent writes). Everything below uses SQLite's `VACUUM INTO`
online backup mechanism instead, which always produces a complete,
self-consistent copy safe to take while the app is running.

## Volume layout

- The SQLite database lives at `/data/agent-task-editor.db` inside the
  container (`DB_PATH`, default `/data/agent-task-editor.db` in
  `docker-compose.yml`/`docker-compose.release.yml`), backed by the named
  volume `db_data`.
- Task attachments live in a separate directory (`UPLOAD_DIR`, default
  `/data/uploads` in the YAML config example — see
  [getting-started.md](getting-started.md)). If attachments matter to you,
  back up that directory too — none of the mechanisms below cover it.

## On-demand backup: `GET /api/v1/backup`

The built-in HTTP endpoint streams a fresh, consistent snapshot generated
via `VACUUM INTO`:

```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/api/v1/backup -o backup-$(date +%F).db
```

This is safe to run at any time, including while the app is under active
write load — unlike a raw file copy, `VACUUM INTO` always produces a
complete, self-consistent snapshot.

The frontend's **Health** page (`/health`) also has a "Download backup"
button that triggers the same endpoint for a one-click manual snapshot from
the browser.

## Manual backup from inside the container

If you'd rather not hit the API (e.g. scripting a backup without an
`API_TOKEN`), you can run the same `VACUUM INTO` command directly inside the
container:

```bash
docker compose exec backend sqlite3 /data/agent-task-editor.db "VACUUM INTO '/data/backup.db'"
docker cp <container>:/data/backup.db ./backup.db
```

Do **not** substitute a plain `docker cp`/`cp` of `/data/agent-task-editor.db`
itself for the `VACUUM INTO` step above — the live file is written in WAL
mode, so a raw copy can miss data still sitting in the `-wal` file, or catch
the database mid-write. `VACUUM INTO` (or the `/api/v1/backup` endpoint,
which uses the same mechanism) is the only way to get a copy that's
guaranteed consistent.

## Automatic local backups

Set `BACKUP_DIR` to enable a built-in scheduler that periodically writes a
rotated `VACUUM INTO` snapshot to a local directory — no external tooling
required:

| Variable | Default | Description |
|---|---|---|
| `BACKUP_DIR` | _(empty)_ | Directory to write snapshots to. Empty = scheduler disabled (on-demand backup via the API/UI above is always available regardless). |
| `BACKUP_INTERVAL` | `24h` | How often to write a new snapshot. Accepts Go duration strings (e.g. `6h`, `24h`). |
| `BACKUP_KEEP` | `7` | Number of most-recent snapshots to retain before pruning older ones. |

Snapshots are named `agent-task-editor-<UTC timestamp>.db`. On each run, the
scheduler writes one new snapshot to `BACKUP_DIR`, then deletes the oldest
snapshots beyond `BACKUP_KEEP`, matching only its own filename pattern —
other files already present in `BACKUP_DIR` are never touched.

A natural place to point `BACKUP_DIR` in the default Docker Compose setup is
a subdirectory of the existing `/data` mount, e.g. `/data/backups` — since
`/data` is already the `db_data` named volume, this persists snapshots
without adding a new volume:

```yaml
environment:
  - BACKUP_DIR=/data/backups
  - BACKUP_INTERVAL=24h
  - BACKUP_KEEP=7
```

Both `docker-compose.yml` and `docker-compose.release.yml` ship this as a
commented-out example.

Whether automatic backups are currently enabled is also surfaced as an
`auto_backup` check on the `GET /api/v1/health/providers` endpoint (and
therefore on the frontend's **Health** page), so a misconfigured or disabled
scheduler is visible at a glance rather than a silent gap.

**This is local-disk rotation only** — snapshots stay on the same host/volume
as the live database, so they don't protect against the loss of that
volume (disk failure, host loss, accidental volume deletion). For
offsite/durable retention, either:

- Point a cron job or sidecar at `BACKUP_DIR` to sync it to remote storage
  (S3, rsync, etc.), or
- Use the Litestream sidecar described below, which continuously replicates
  the *live* database (not just periodic snapshots) to S3-compatible storage.

## Litestream sidecar (continuous offsite replication)

For continuous, near-real-time replication to S3-compatible storage, run
[Litestream](https://litestream.io/) as a sidecar container watching the
live database file. This is illustrative documentation, not a first-class
shipped feature — you'll need to add the sidecar to your own compose file.

`litestream.yml` (mount into the sidecar container):

```yaml
dbs:
  - path: /data/agent-task-editor.db
    replicas:
      - type: s3
        bucket: ${LITESTREAM_S3_BUCKET}
        path: agent-task-editor
        endpoint: ${LITESTREAM_S3_ENDPOINT}   # omit for real AWS S3
        access-key-id: ${LITESTREAM_S3_ACCESS_KEY_ID}
        secret-access-key: ${LITESTREAM_S3_SECRET_ACCESS_KEY}
```

Compose snippet adding the sidecar (shares the `db_data` volume with
`backend` read-only, so it can tail the live WAL without racing writes):

```yaml
services:
  litestream:
    image: litestream/litestream:latest
    command: replicate
    volumes:
      - db_data:/data:ro
      - ./litestream.yml:/etc/litestream.yml:ro
    environment:
      - LITESTREAM_S3_BUCKET=${LITESTREAM_S3_BUCKET}
      - LITESTREAM_S3_ENDPOINT=${LITESTREAM_S3_ENDPOINT:-}
      - LITESTREAM_S3_ACCESS_KEY_ID=${LITESTREAM_S3_ACCESS_KEY_ID}
      - LITESTREAM_S3_SECRET_ACCESS_KEY=${LITESTREAM_S3_SECRET_ACCESS_KEY}
```

Litestream continuously ships WAL segments to the bucket, so recovery point
objective (RPO) is measured in seconds, not the `BACKUP_INTERVAL` of the
scheduled local snapshots above. See the
[Litestream docs](https://litestream.io/getting-started/) for restore
instructions (`litestream restore`), which are separate from the "replace
the file and start the backend" procedure below.

## Restore procedure

Whether you're restoring a snapshot from `/api/v1/backup`, the automatic
scheduler, or a manual `VACUUM INTO`, the procedure is the same — replace
the live database file and restart:

1. Stop the backend so it releases the SQLite file:
   ```bash
   docker compose stop backend
   ```
2. Replace the volume-mounted database file with your backup. One way,
   without needing a running container:
   ```bash
   docker run --rm -v db_data:/data -v $(pwd):/backup alpine \
     cp /backup/backup.db /data/agent-task-editor.db
   ```
   (Or `docker cp your-backup.db <container>:/data/agent-task-editor.db`
   against a stopped-but-not-removed container.)
3. Remove any stale `-wal`/`-shm` sidecar files in the volume if present —
   these are only relevant if you're restoring a raw file-level copy rather
   than a `VACUUM INTO` snapshot (which never has them, since the export is
   already a single consistent file).
4. Start the backend again:
   ```bash
   docker compose start backend
   ```
   File ownership self-heals on startup — `entrypoint.sh` chowns `/data` to
   the configured `PUID`/`PGID` on every container start, regardless of
   which tool wrote the replacement file.
   Restoring an **older** snapshot onto a **newer** binary is expected to
   just work: migrations are additive and idempotent (`golang-migrate` only
   applies migrations newer than what the snapshot's `schema_migrations`
   table records), so the backend brings an older snapshot's schema up to
   date automatically on next start.

Before relying on this procedure in production, run through it once against
your own `docker compose` stack: take a `VACUUM INTO` snapshot, stop the
backend, swap the snapshot in, restart, and confirm the app comes up
cleanly with migrations applying automatically and existing data intact.
