-- Target label for ghsync PR-feedback auto-transition; '' = use the failure path.
ALTER TABLE workflow_labels ADD COLUMN pr_feedback_label TEXT NOT NULL DEFAULT '';
