# 20. sqlc query-writing constraints

Status: Accepted

## Context

The pinned sqlc SQLite analyzer assumes byte offsets where it should use rune
offsets, and mishandles some parameter shapes. Symptoms are silent: no codegen
error, only a runtime `near "...": syntax error`, or a per-query row struct
instead of `gen.Task`.

## Decision

- `queries/*.sql` are strictly ASCII. A single multi-byte character (an em dash
  in a comment) corrupts the generated SQL strings that follow it in the file.
  Check: `grep -lP '[^\x00-\x7F]' internal/storage/queries/*.sql` must print
  nothing. Prefer a new per-table file over appending to `tasks.sql`.
  Migrations are read as schema and have proven tolerant, but keep them ASCII.
- Long or paginated queries (`SearchTasksPage`, `ListAgentLogsPage`, the
  `...Page` config queries) use positional `?N` params, not `@named`, with the
  argument order documented in the query comment; they use `SELECT t.*` to keep
  query text short.
- The workflow engine's transition write is a raw-SQL compare-and-swap on the
  tx (it needs `AND label = ?`, which sqlc miscompiles), not the generated
  `UpdateTaskLabel`.
- When adding a `tasks` column, append it to every explicit column list in
  `tasks.sql` (including `t.`-prefixed `ListAgentPickupTasks`), or sqlc emits a
  per-query struct.
- `SearchTasks` params that use the `@x = '' OR col = @x` idiom are typed
  `interface{}`; pass strings.
- CI regenerates with the pinned sqlc and fails on `git diff`.

## Consequences

- Always run `sqlc generate` and commit `gen/`; never hand-edit it.
