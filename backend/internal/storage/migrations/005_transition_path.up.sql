ALTER TABLE workflow_transitions ADD COLUMN path TEXT CHECK (path IN ('success', 'failure', 'either'));
