-- Peer task dependencies: "don't dispatch B until A is done".
-- Many-to-many; a task may depend on several tasks and block several others.
-- Both foreign keys cascade on delete: deleting a task removes its edges, which
-- unblocks any dependents (deletion is already a deliberate human act).
CREATE TABLE task_dependencies (
    task_id            TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, depends_on_task_id)
);

CREATE INDEX idx_task_deps_task    ON task_dependencies(task_id);
CREATE INDEX idx_task_deps_depends ON task_dependencies(depends_on_task_id);
