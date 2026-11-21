-- head_sha is the worktree HEAD commit a verification ran against (empty when
-- unknown). reused_from_run_id is set when the pass was reused from an earlier
-- run's pass on the same commit and command instead of re-running the command.
-- Plain TEXT, no FK: deleting the source run must not cascade here.
ALTER TABLE run_verifications ADD COLUMN head_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE run_verifications ADD COLUMN reused_from_run_id TEXT;
