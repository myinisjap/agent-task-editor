# Backend

Go 1.24 HTTP server. Module: `github.com/myinisjap/agent-task-editor/backend`.

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
internal/agent/   Provider interface, ClaudeRunner, AnthropicRunner, LLMRunner, Pool, Dispatcher
internal/api/     Chi router, all HTTP handlers, middleware
internal/config/  Config struct; loaded from YAML file then env vars override
internal/ghclient/ Thin wrapper around the `gh` CLI (PR create/lookup, issue listing, GitHub URL parsing)
internal/ghsync/  Background PR-state poller — refreshes task git_state from GitHub, cleans up merged branches
internal/health/  Provider readiness checks (used by GET /health/providers)
internal/storage/ SQLite DB, migrations, sqlc-generated queries, seed data
internal/tasksource/ GitHub Issues importer — polls opted-in repos and creates tasks for new issues
internal/workflow/ State machine engine — validates/executes label transitions
internal/ws/      WebSocket hub + per-client connection management
```

## Code Generation

SQL queries live in `internal/storage/queries/*.sql`. After editing them:

```bash
sqlc generate   # or: go generate ./...
```

Generated output goes to `internal/storage/gen/`. Do not hand-edit files in `gen/`.

CI runs `sqlc generate` (pinned to the sqlc version that produced the committed
`gen/` output) and fails the build if `git diff` finds any drift — always run
`sqlc generate` and commit the result after editing queries or migrations.

CI also runs `govulncheck ./...` against the backend module. It currently
reports reachable stdlib CVEs fixed only in Go 1.25.8+ (this repo is pinned to
Go 1.24), so the step is non-blocking (`continue-on-error: true`) until the Go
toolchain is upgraded — see the comment in `.github/workflows/ci.yml`.

## Database Migrations

Files in `internal/storage/migrations/` follow the `NNN_name.up.sql` / `NNN_name.down.sql` pattern. Migrations run automatically at startup via `golang-migrate`. To add a migration:

1. Create `NNN_description.up.sql` and `NNN_description.down.sql`
2. Update `internal/storage/queries/*.sql` if new columns/tables are involved
3. Run `sqlc generate` to update `gen/`
4. Update `gen/models.go` struct if adding columns (sqlc handles this, but verify)

## Testing

Tests are in `*_test.go` files alongside the packages. The storage tests use an in-memory SQLite instance. Run a specific package:

```bash
go test ./internal/workflow/...
go test -v ./internal/api/handlers/...
```

## Container Toolchain

`Dockerfile`'s final stage (`FROM node:22-alpine`) is what executes agent `Bash`/`run_bash` commands against bind-mounted repos in production — not just this project's own build. It currently includes:

- **Go 1.24**, copied from this same Dockerfile's `golang:1.24-alpine` builder stage (`COPY --from=builder /usr/local/go /usr/local/go`) so the Go version agents see always matches what builds `bin/server`/`bin/mcp-server`. `GOPATH`/`GOCACHE`/`GOMODCACHE` point at writable dirs under `/home/node`.
- **Node 22 / npm**, inherited from the base image — covers Vite/React/TS repos (`npm ci`, `npm run build`, `npm test`).
- **`build-base`** (gcc/g++/make/musl-dev) for cgo (this backend's `mattn/go-sqlite3` dependency) and native npm addon compilation.

To add another language for agents to use, edit the *final* stage of `Dockerfile` (not the builder stage — that only compiles this repo's own Go binaries) and rebuild with `docker compose build backend`. See `../docs/getting-started.md#supported-languages--extending-the-toolchain` for the full guide and Alpine/glibc caveats.
