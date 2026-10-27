CREATE TABLE IF NOT EXISTS repo_lessons (
    id              TEXT PRIMARY KEY,
    repo_id         TEXT REFERENCES repos(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    body            TEXT NOT NULL,
    path_globs      TEXT NOT NULL DEFAULT '',
    stages          TEXT NOT NULL DEFAULT '',
    citations       TEXT NOT NULL DEFAULT '[]',
    source          TEXT NOT NULL DEFAULT 'human',
    source_task_id  TEXT,
    status          TEXT NOT NULL DEFAULT 'active',
    injected_count  INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_checked_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_repo_lessons_repo_status ON repo_lessons(repo_id, status);
