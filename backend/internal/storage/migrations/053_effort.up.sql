-- Optional per-agent-config reasoning effort level, passed through to
-- providers that support tunable reasoning effort. Empty string means
-- "unset" (provider default). See docs/providers/claude.md and
-- docs/providers/codex_cli.md for the per-provider mapping.
ALTER TABLE agent_configs ADD COLUMN effort TEXT NOT NULL DEFAULT '';
