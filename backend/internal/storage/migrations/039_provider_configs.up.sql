-- Provider Config: the provider/model/API-key (env vars) triple, split out of
-- Agent Config so it can be shared/reused by chat sessions too. Agent Config
-- keeps workflow-behavior fields (system prompt, labels, retry policy,
-- plugins, command filters, subtasks, cost caps) and now references a
-- Provider Config by id instead of inlining provider/model/env. Chat Session
-- keeps its own identity fields (repo, worktree, provider-side session id)
-- and now references a Provider Config too instead of inlining provider/model.
CREATE TABLE provider_configs (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    provider   TEXT NOT NULL,
    model      TEXT NOT NULL DEFAULT '',
    env        TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Backfill: one provider_config per existing agent_config, reusing the
-- agent_config's own id as the provider_config's id (both are already
-- unique, and this avoids any ambiguity zipping two independent inserts
-- together).
INSERT INTO provider_configs (id, name, provider, model, env, created_at, updated_at)
SELECT id, name || ' (provider)', provider, model, env, created_at, updated_at
FROM agent_configs;

-- Same treatment for chat_sessions (no env column exists there; chat
-- sessions don't carry their own API keys).
INSERT INTO provider_configs (id, name, provider, model, env, created_at, updated_at)
SELECT id, COALESCE(NULLIF(title, ''), 'chat') || ' (provider)', provider, model, '{}', created_at, updated_at
FROM chat_sessions;

-- SQLite's ALTER TABLE can't add a NOT NULL column with no constant default
-- to a non-empty table, nor combine a REFERENCES clause with a non-NULL
-- DEFAULT on ADD COLUMN ("Cannot add a REFERENCES column with non-NULL
-- default value"), and it has no ALTER COLUMN to tighten a constraint
-- afterwards either. So rebuild both tables (same pattern as
-- 008_agent_runs_fk_set_null): recreate with provider_config_id as a proper
-- NOT NULL REFERENCES column in place of provider/model(/env), copy the data
-- across (every row already has its backfilled provider_configs.id, which is
-- just its own id), then swap the table in.
CREATE TABLE agent_configs_new (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    system_prompt       TEXT NOT NULL DEFAULT '',
    labels              TEXT NOT NULL DEFAULT '[]',
    max_tokens          INTEGER NOT NULL DEFAULT 8192,
    timeout_secs        INTEGER NOT NULL DEFAULT 600,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    enabled             INTEGER NOT NULL DEFAULT 1,
    enabled_plugins     TEXT NOT NULL DEFAULT '[]',
    enabled_mcp_servers TEXT NOT NULL DEFAULT '[]',
    max_turns           INTEGER NOT NULL DEFAULT 50,
    command_allowlist   TEXT NOT NULL DEFAULT '[]',
    command_denylist    TEXT NOT NULL DEFAULT '[]',
    max_retries         INTEGER NOT NULL DEFAULT 3,
    retry_backoff_secs  INTEGER NOT NULL DEFAULT 30,
    resume_sessions     INTEGER NOT NULL DEFAULT 1,
    subtasks_enabled    INTEGER NOT NULL DEFAULT 0,
    max_subtasks        INTEGER NOT NULL DEFAULT 10,
    max_cost_usd        REAL NOT NULL DEFAULT 0,
    priority            INTEGER NOT NULL DEFAULT 0,
    provider_config_id  TEXT NOT NULL REFERENCES provider_configs(id)
);

INSERT INTO agent_configs_new (
    id, name, system_prompt, labels, max_tokens, timeout_secs, created_at, updated_at,
    enabled, enabled_plugins, enabled_mcp_servers, max_turns, command_allowlist, command_denylist,
    max_retries, retry_backoff_secs, resume_sessions, subtasks_enabled, max_subtasks, max_cost_usd,
    priority, provider_config_id
)
SELECT
    id, name, system_prompt, labels, max_tokens, timeout_secs, created_at, updated_at,
    enabled, enabled_plugins, enabled_mcp_servers, max_turns, command_allowlist, command_denylist,
    max_retries, retry_backoff_secs, resume_sessions, subtasks_enabled, max_subtasks, max_cost_usd,
    priority, id
FROM agent_configs;

DROP TABLE agent_configs;
ALTER TABLE agent_configs_new RENAME TO agent_configs;

CREATE INDEX idx_agent_configs_provider_config ON agent_configs(provider_config_id);

CREATE TABLE chat_sessions_new (
    id                  TEXT PRIMARY KEY,
    repo_id             TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    title               TEXT NOT NULL DEFAULT '',
    provider_session_id TEXT NOT NULL DEFAULT '',
    worktree_path       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    provider_config_id  TEXT NOT NULL REFERENCES provider_configs(id)
);

INSERT INTO chat_sessions_new (
    id, repo_id, title, provider_session_id, worktree_path, created_at, updated_at, provider_config_id
)
SELECT
    id, repo_id, title, provider_session_id, worktree_path, created_at, updated_at, id
FROM chat_sessions;

DROP TABLE chat_sessions;
ALTER TABLE chat_sessions_new RENAME TO chat_sessions;

CREATE INDEX idx_chat_sessions_repo ON chat_sessions(repo_id);
CREATE INDEX idx_chat_sessions_provider_config ON chat_sessions(provider_config_id);
