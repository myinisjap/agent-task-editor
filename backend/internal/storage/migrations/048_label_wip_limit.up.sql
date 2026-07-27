ALTER TABLE workflow_labels ADD COLUMN wip_limit INTEGER;
ALTER TABLE workflow_labels ADD COLUMN wip_limit_hard INTEGER NOT NULL DEFAULT 0;
