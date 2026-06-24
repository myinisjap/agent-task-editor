# cmd/server

Main server entrypoint. Reads config, opens the database, wires all subsystems together, and starts the HTTP server.

## Startup Sequence

1. Load config from `CONFIG_FILE` (YAML) then env var overrides
2. Open SQLite database with WAL mode enabled
3. Run database migrations
4. Seed default workflow if no workflows exist
5. Mark any `running` agent runs as `failed` (crash recovery)
6. Clear all `active_agent_run_id` values (pool has restarted; nothing is genuinely active)
7. Create the WebSocket hub
8. Create the workflow engine (hub is the Publisher)
9. Create the agent worker pool + dispatcher
10. Build the Chi router
11. Start pool goroutine, dispatcher goroutine, HTTP server goroutine
12. Wait for SIGINT/SIGTERM, then graceful shutdown (10s timeout)

## Crash Recovery

Steps 5 and 6 ensure a clean state after an unclean shutdown:
- Runs stuck in `running` become `failed` so the dispatcher can re-dispatch
- `active_agent_run_id` is cleared so no tasks are permanently locked

## Configuration

Loaded by `internal/config`. See `backend/internal/config/CLAUDE.md` for the full field list.
