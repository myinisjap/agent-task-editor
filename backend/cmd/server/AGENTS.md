# cmd/server

Main entrypoint: reads config, opens the database, wires all subsystems, starts HTTP.

## Startup Sequence

1. Load config from `CONFIG_FILE` (YAML), then env var overrides (`internal/config/AGENTS.md`)
2. Open SQLite (WAL); take the pre-migration snapshot if migrations are pending; run migrations
3. Seed the default workflow if none exist
4. Crash recovery: mark `running`/`pending` runs `interrupted` (`storage.RecoverInterruptedRuns`) and clear all `active_agent_run_id` values (ADR 0026)
5. Create the WS hub, workflow engine (hub is the Publisher), worker pool and dispatcher, then the Chi router
6. Start pool, dispatcher and HTTP goroutines
7. On SIGINT/SIGTERM: stop dispatch, drain in-flight runs for `SHUTDOWN_DRAIN_TIMEOUT`, cancel the rest as `interrupted`, wait for the pool, then close the DB (ADR 0026)

Don't reintroduce `failed` for crash or shutdown recovery. `cmd/server` is excluded from the CI coverage metric (ADR 0035); keep logic that needs tests in `internal/` packages.
