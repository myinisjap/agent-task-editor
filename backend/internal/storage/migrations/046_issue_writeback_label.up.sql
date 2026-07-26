-- Per-repo override for the label applied to the source GitHub issue when an
-- imported task first leaves the workflow's human-gate label. Empty string
-- (the default) falls back to writeback.InProgressLabel ("agent-in-progress").
ALTER TABLE repos ADD COLUMN issue_writeback_label TEXT NOT NULL DEFAULT '';
