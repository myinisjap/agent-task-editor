# Backend

Go HTTP server (language floor in `go.mod`; CI and the Docker builder use a newer toolchain). Module: `github.com/myinisjap/agent-task-editor/backend`.

## Build & Run

```bash
go run ./cmd/server           # dev
go build -o server ./cmd/server  # production binary
go test ./...                  # all tests
go generate ./...              # regenerate sqlc code after SQL changes
```

## Key Dependencies

- `github.com/go-chi/chi/v5` — HTTP router
- `github.com/mattn/go-sqlite3` — SQLite driver (cgo)
- `github.com/golang-migrate/migrate/v4` — database migrations
- `github.com/sqlc-dev/sqlc` — SQL→Go codegen (dev tool, see `sqlc.yaml`)
- `nhooyr.io/websocket` — WebSocket library
- `github.com/google/uuid` — UUIDs
- `gopkg.in/yaml.v3` — config + workflow YAML

## Package Map

```
cmd/server/       Main binary — wires config, DB, engine, pool, dispatcher, router
cmd/mcp-server/   Standalone MCP sidecar — stdio JSON-RPC 2.0
internal/agent/   Provider interface, Pool, Dispatcher (concrete runners in `providers/`)
internal/api/     Chi router, all HTTP handlers, middleware
internal/backup/  Optional scheduler: periodic rotated local DB snapshots. Enabled via BACKUP_DIR (restart required); interval/keep are DB-backed (backup_settings table, seeded from BACKUP_INTERVAL/BACKUP_KEEP) and editable at runtime via PUT /api/v1/backup/settings, no restart needed
internal/config/  Config struct; loaded from YAML file then env vars override
internal/ghclient/ Thin wrapper around the `gh` CLI (PR create/lookup, PR head/mergeability, issue listing, PR reviews/review comments/check runs, GitHub URL parsing)
internal/ghsync/  Background PR-state poller — refreshes task git_state + pr_mergeable from GitHub, cleans up merged branches, ingests PR review/GHA/merge-conflict feedback into task_review_comments + run Feedback (optional auto-transition; see pr_review.go)
internal/health/  Provider readiness checks (used by GET /health/providers)
internal/prflow/  Push a task's branch and create/find its PR (forge resolution, PR body, SetTaskPR persist, issue write-back); shared by the HTTP CreatePR handler and the workflow OnCreatePR hook wired in cmd/server. Must not import api/handlers
internal/storage/ SQLite DB, migrations, sqlc-generated queries, seed data
internal/tasksource/ GitHub Issues importer — polls opted-in repos, creates tasks for new issues, updates tasks whose issue drifted, reconciles tasks whose issue closed/unlabeled, and (opt-in) ingests the issue comment thread
internal/workflow/ State machine engine — validates/executes label transitions
internal/ws/      WebSocket hub + per-client connection management
```

## Code Generation

SQL queries live in `internal/storage/queries/*.sql`. After editing them:

```bash
sqlc generate   # or: go generate ./...
```

Generated output goes to `internal/storage/gen/`. Do not hand-edit files in `gen/`.

CI runs `sqlc generate` (pinned version) and fails on any `git diff` — always run it and commit the result after editing queries or migrations. CI also runs `govulncheck ./...` (blocking); it analyzes the stdlib of the running toolchain, not the `go.mod` directive (see `.github/workflows/ci.yml`). Query-writing constraints (ASCII only, positional params, new `tasks` columns) are in `internal/storage/AGENTS.md` (ADR 0020, 0035).

## Database Migrations

Files in `internal/storage/migrations/` follow the `NNN_name.up.sql` / `NNN_name.down.sql` pattern. Migrations run automatically at startup via `golang-migrate`. To add a migration:

1. Create `NNN_description.up.sql` and `NNN_description.down.sql`
2. Update `internal/storage/queries/*.sql` if new columns/tables are involved
3. Run `sqlc generate` to update `gen/`
4. Update `gen/models.go` struct if adding columns (sqlc handles this, but verify)

## Timestamps

All DATETIME/TIMESTAMP columns are UTC `YYYY-MM-DD HH:MM:SS`; `storage.Open` converts bound `time.Time` args via `storage.FormatTime`. Compare and `ORDER BY` as plain strings (no `datetime()` wrapping); build hand-made strings with `storage.FormatTime(t)`. ADR 0021.

## Testing

Tests are in `*_test.go` files alongside the packages. The storage tests use an in-memory SQLite instance. Run a specific package:

```bash
go test ./internal/workflow/...
go test -v ./internal/api/handlers/...
```

CI enforces a coverage floor (`.github/workflows/ci.yml`, "Enforce coverage floor") computed without `internal/storage/gen/` and `cmd/server` (ADR 0035), so local totals read a few points lower. Reproduce CI's number:

```bash
go test -race -coverprofile=coverage.out -covermode=atomic ./...
grep -vE '/internal/storage/gen/|/cmd/server/' coverage.out > coverage.filtered.out
go tool cover -func=coverage.filtered.out | tail -1
```

## Container Toolchain

`Dockerfile`'s **final** stage (not the builder) is what executes agent `Bash` commands against bind-mounted repos. It carries Go (copied from the builder stage for version parity), Node/npm, `build-essential`, and pinned `mise`/`uv` (per-repo runtime pins, `internal/agent/runtime/`). To add a language for every agent, edit the final stage and run `docker compose build backend` (`../docs/getting-started.md#supported-languages--extending-the-toolchain`); for one repo prefer a runtime pin (`../docs/runtime.md`). ADR 0006.
