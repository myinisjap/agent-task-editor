-- Task templates: reusable pre-filled title/description/type for recurring
-- shapes of work (e.g. "upgrade dependency X", "fix flaky test").
CREATE TABLE task_templates (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    type        TEXT NOT NULL DEFAULT 'feature',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
