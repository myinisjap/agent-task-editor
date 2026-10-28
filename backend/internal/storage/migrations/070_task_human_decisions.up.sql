CREATE TABLE IF NOT EXISTS task_human_decisions (
    run_id            TEXT PRIMARY KEY,
    task_id           TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    label             TEXT NOT NULL DEFAULT '',
    escalation_reason TEXT NOT NULL DEFAULT '',
    question          TEXT NOT NULL DEFAULT '',
    asked_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reply             TEXT,
    reply_run_id      TEXT,
    replied_at        DATETIME
);
CREATE INDEX IF NOT EXISTS idx_task_human_decisions_task ON task_human_decisions(task_id, replied_at);
