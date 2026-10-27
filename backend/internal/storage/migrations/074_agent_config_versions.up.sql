-- Agent config version history: a numbered snapshot of the behaviour-relevant
-- fields, written whenever an update changes any of them.
CREATE TABLE agent_config_versions (
    id              TEXT PRIMARY KEY,
    agent_config_id TEXT NOT NULL REFERENCES agent_configs(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,
    snapshot        TEXT NOT NULL,
    actor_id        TEXT,
    note            TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (agent_config_id, version)
);

-- Config version a run was dispatched with (NULL = before version history).
ALTER TABLE agent_runs ADD COLUMN agent_config_version INTEGER;

-- Backfill v1 for every existing config.
INSERT INTO agent_config_versions (id, agent_config_id, version, snapshot, created_at, note)
SELECT lower(hex(randomblob(16))), id, 1, json_object(
   'system_prompt', system_prompt,
   'provider_config_id', provider_config_id,
   'fallback_provider_config_ids', CASE WHEN json_valid(fallback_provider_config_ids) THEN json(fallback_provider_config_ids) ELSE json('[]') END,
   'escalation_ladder', CASE WHEN json_valid(escalation_ladder) THEN json(escalation_ladder) ELSE json('[]') END,
   'effort', effort, 'permission_mode', permission_mode,
   'max_turns', max_turns, 'max_tokens', max_tokens, 'timeout_secs', timeout_secs,
   'max_cost_usd', max_cost_usd, 'max_retries', max_retries, 'retry_backoff_secs', retry_backoff_secs,
   'resume_sessions', CASE WHEN resume_sessions != 0 THEN json('true') ELSE json('false') END,
   'subtasks_enabled', CASE WHEN subtasks_enabled != 0 THEN json('true') ELSE json('false') END,
   'max_subtasks', max_subtasks,
   'enabled_plugins', CASE WHEN json_valid(enabled_plugins) THEN json(enabled_plugins) ELSE json('[]') END,
   'enabled_mcp_servers', CASE WHEN json_valid(enabled_mcp_servers) THEN json(enabled_mcp_servers) ELSE json('[]') END,
   'command_allowlist', CASE WHEN json_valid(command_allowlist) THEN json(command_allowlist) ELSE json('[]') END,
   'command_denylist', CASE WHEN json_valid(command_denylist) THEN json(command_denylist) ELSE json('[]') END),
 updated_at, 'Initial version (backfilled)'
FROM agent_configs;
