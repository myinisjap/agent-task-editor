CREATE TABLE workflows (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workflow_labels (
    id           TEXT PRIMARY KEY,
    workflow_id  TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    color        TEXT NOT NULL DEFAULT '#6B7280',
    sort_order   INTEGER NOT NULL DEFAULT 0,
    agent_ignore INTEGER NOT NULL DEFAULT 0,
    is_terminal  INTEGER NOT NULL DEFAULT 0,
    UNIQUE(workflow_id, name)
);

CREATE TABLE agent_configs (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    provider      TEXT NOT NULL,
    model         TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    labels        TEXT NOT NULL DEFAULT '[]',
    env           TEXT NOT NULL DEFAULT '{}',
    max_tokens    INTEGER NOT NULL DEFAULT 8192,
    timeout_secs  INTEGER NOT NULL DEFAULT 600,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workflow_transitions (
    id              TEXT PRIMARY KEY,
    workflow_id     TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    from_label      TEXT NOT NULL,
    to_label        TEXT NOT NULL,
    trigger_type    TEXT NOT NULL CHECK (trigger_type IN ('agent', 'human', 'both')),
    agent_config_id TEXT REFERENCES agent_configs(id) ON DELETE SET NULL,
    UNIQUE(workflow_id, from_label, to_label)
);

CREATE TABLE repos (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    remote_url  TEXT,
    workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tasks (
    id                   TEXT PRIMARY KEY,
    title                TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    type                 TEXT NOT NULL DEFAULT 'feature',
    label                TEXT NOT NULL DEFAULT 'not_ready',
    repo_id              TEXT NOT NULL REFERENCES repos(id),
    workflow_id          TEXT NOT NULL REFERENCES workflows(id),
    current_agent_run_id TEXT,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE task_label_history (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    from_label TEXT,
    to_label   TEXT NOT NULL,
    trigger    TEXT NOT NULL,
    actor_id   TEXT,
    note       TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE agent_runs (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_config_id TEXT NOT NULL REFERENCES agent_configs(id),
    status          TEXT NOT NULL DEFAULT 'pending',
    feedback        TEXT,
    started_at      DATETIME,
    completed_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE agent_logs (
    id           TEXT PRIMARY KEY,
    agent_run_id TEXT NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    timestamp    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    type         TEXT NOT NULL,
    content      TEXT NOT NULL
);

CREATE INDEX idx_tasks_label     ON tasks(label);
CREATE INDEX idx_tasks_repo      ON tasks(repo_id);
CREATE INDEX idx_agent_runs_task ON agent_runs(task_id);
CREATE INDEX idx_agent_logs_run  ON agent_logs(agent_run_id);
CREATE INDEX idx_history_task    ON task_label_history(task_id);
