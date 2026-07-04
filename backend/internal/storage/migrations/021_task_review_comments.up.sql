CREATE TABLE task_review_comments (
    id                 TEXT PRIMARY KEY,
    task_id            TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    file_path          TEXT NOT NULL,
    side               TEXT NOT NULL DEFAULT 'new' CHECK (side IN ('old', 'new')),
    start_line         INTEGER NOT NULL,
    end_line           INTEGER NOT NULL,
    quoted_text        TEXT NOT NULL DEFAULT '',
    body               TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    resolution_note    TEXT,
    resolved_by_run_id TEXT REFERENCES agent_runs(id) ON DELETE SET NULL,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_review_comments_task ON task_review_comments(task_id, status);
