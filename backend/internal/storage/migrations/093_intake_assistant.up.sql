-- Task intake assistant: an optional, advisory model check of a new task
-- (clarity verdict, suggested type/workflow, real duplicates). Off by default;
-- the deterministic duplicate check needs no storage (computed on read).
-- No REFERENCES on provider_config_id (same reason as autonomy_settings).
CREATE TABLE intake_assistant_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    enabled            INTEGER NOT NULL DEFAULT 0,
    auto_check         INTEGER NOT NULL DEFAULT 1,
    provider_config_id TEXT,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO intake_assistant_settings (id) VALUES (1);

-- The latest check per task.
CREATE TABLE task_intake_checks (
    task_id               TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    status                TEXT NOT NULL CHECK (status IN ('running','done','failed')),
    trigger               TEXT NOT NULL DEFAULT 'manual',
    verdict               TEXT NOT NULL DEFAULT '',
    questions             TEXT NOT NULL DEFAULT '[]',
    suggested_type        TEXT NOT NULL DEFAULT '',
    suggested_workflow_id TEXT NOT NULL DEFAULT '',
    duplicate_ids         TEXT NOT NULL DEFAULT '[]',
    error                 TEXT NOT NULL DEFAULT '',
    provider_config_id    TEXT,
    cost_usd              REAL NOT NULL DEFAULT 0,
    input_tokens          INTEGER NOT NULL DEFAULT 0,
    output_tokens         INTEGER NOT NULL DEFAULT 0,
    started_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at           DATETIME
);
