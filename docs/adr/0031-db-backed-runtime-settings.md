# 31. Runtime-tunable settings live in the database; env vars only seed them

Status: Accepted

## Context

Operators want to change backup cadence, cost-warning ratio and log retention
without restarting the server. Environment variables cannot change at runtime.

## Decision

Such settings are DB rows (singleton tables, e.g. `backup_settings` with
`CHECK (id = 1)`) read and written through `/api/v1/.../settings`. Env vars seed
the initial row only (`BACKUP_INTERVAL`/`BACKUP_KEEP`). The backup scheduler
re-reads its settings before each run using a re-armed `time.Timer`, not a
`Ticker`, and enforces `backup.MinInterval` (10 min) again as a last line of
defense. Settings that change deployment shape stay config-only and need a
restart (`BACKUP_DIR`). A pre-migration `VACUUM INTO` snapshot protects the DB
before pending migrations run.

## Consequences

- Handlers validate (interval >= 600s, keep >= 1) and the consumer re-validates.
