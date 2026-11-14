-- Multi-repo stage 1 (docs/adr/0041-multi-repo-work-as-parent-with-per-repo-children.md):
-- opt-in flag letting an agent's create_subtask target another repo.
ALTER TABLE agent_configs ADD COLUMN cross_repo_subtasks_enabled INTEGER NOT NULL DEFAULT 0;
