-- Optional per-repo cap on concurrent agent runs. NULL (the default) means
-- "no repo-specific cap" — the dispatcher falls back to the global
-- MAX_WORKERS limit, preserving today's behavior exactly. A repo with many
-- eligible tasks can be capped here so it can't monopolize every worker slot
-- and starve every other repo (see docs/agents.md).
ALTER TABLE repos ADD COLUMN max_concurrent_runs INTEGER;
