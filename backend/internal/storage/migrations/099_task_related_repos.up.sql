-- ADR 0041 stage 4: repos a human/API caller named when creating a task (related_repo_ids
-- plus foreign children's repos). Per-task opt-in for cross-repo create_subtask and the
-- planner prompt's repo list. Side table (ADR 0037), not a tasks column.
CREATE TABLE task_related_repos (
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  repo_id    TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (task_id, repo_id)
);
