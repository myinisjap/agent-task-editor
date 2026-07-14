-- Recurring task schedules: fire a task_template on a cron schedule against a
-- repo. A scheduler sweep creates a task (source = 'schedule', source_ref =
-- schedule id) when the schedule is due and no open task from the same
-- schedule already exists, so an unfinished run is never stacked on top of.
CREATE TABLE task_schedules (
    id             TEXT PRIMARY KEY,
    template_id    TEXT NOT NULL REFERENCES task_templates(id) ON DELETE CASCADE,
    repo_id        TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    cron_expr      TEXT NOT NULL,
    target_label   TEXT NOT NULL DEFAULT 'not_ready',
    enabled        BOOLEAN NOT NULL DEFAULT 1,
    last_run_at    DATETIME,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_task_schedules_enabled ON task_schedules(enabled);
