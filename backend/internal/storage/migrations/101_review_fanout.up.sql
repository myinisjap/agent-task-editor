-- Review fan-out: opt-in flag + thresholds for splitting a large-diff review
-- into focused reviewer passes (docs/agents.md "Review fan-out").
ALTER TABLE agent_configs ADD COLUMN review_fanout_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_configs ADD COLUMN review_fanout_min_lines INTEGER NOT NULL DEFAULT 400;
ALTER TABLE agent_configs ADD COLUMN review_fanout_min_files INTEGER NOT NULL DEFAULT 15;
