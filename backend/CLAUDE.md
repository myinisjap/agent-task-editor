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
internal/storage/ SQLite DB, migrations, sqlc-generated queries, seed data
internal/workflow/ State machine engine — validates/executes label transitions
internal/ws/      WebSocket hub + per-client connection management
```

## Code Generation

SQL queries live in `internal/storage/queries/*.sql`. After editing them:

```bash
sqlc generate   # or: go generate ./...
```

Generated output goes to `internal/storage/gen/`. Do not hand-edit files in `gen/`.

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
