-- Autonomy dial 6: escape details, ask rules, gate change history, step-down settings.
-- No REFERENCES on the new escape columns: SQLite cannot DROP COLUMN an FK column in the down.
ALTER TABLE autonomy_escapes ADD COLUMN decision_id TEXT;
ALTER TABLE autonomy_escapes ADD COLUMN actor TEXT;
ALTER TABLE autonomy_escapes ADD COLUMN note TEXT;
ALTER TABLE autonomy_escapes ADD COLUMN rule_id TEXT;
ALTER TABLE autonomy_escapes ADD COLUMN reset_by_change_id TEXT;
CREATE INDEX idx_autonomy_escapes_gate ON autonomy_escapes(workflow_id, label, created_at);
CREATE INDEX idx_autonomy_escapes_decision ON autonomy_escapes(decision_id);

CREATE TABLE autonomy_ask_rules (
    id                TEXT PRIMARY KEY,
    repo_id           TEXT REFERENCES repos(id) ON DELETE CASCADE,
    workflow_id       TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    label             TEXT,
    path_globs        TEXT NOT NULL DEFAULT '[]',
    task_type         TEXT,
    min_changed_lines INTEGER,
    created_by        TEXT NOT NULL DEFAULT '',
    source_task_id    TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    hit_count         INTEGER NOT NULL DEFAULT 0,
    last_hit_at       DATETIME
);
CREATE INDEX idx_autonomy_ask_rules_wf ON autonomy_ask_rules(workflow_id, label);

CREATE TABLE autonomy_changes (
    id          TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    label       TEXT NOT NULL,
    from_mode   TEXT NOT NULL CHECK (from_mode IN ('ask','on_flags','auto')),
    to_mode     TEXT NOT NULL CHECK (to_mode IN ('ask','on_flags','auto')),
    actor       TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_autonomy_changes_gate ON autonomy_changes(workflow_id, label, created_at);

CREATE TABLE autonomy_settings (
    id                    INTEGER PRIMARY KEY CHECK (id = 1),
    step_down_escapes     INTEGER NOT NULL DEFAULT 2,
    step_down_window_days INTEGER NOT NULL DEFAULT 14,
    updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO autonomy_settings (id, step_down_escapes, step_down_window_days) VALUES (1, 2, 14);
