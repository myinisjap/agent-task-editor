-- Mergeability of a task's open PR against its base branch, as reported by
-- GitHub and refreshed by the ghsync sweep. One of '' (not known / no PR yet),
-- 'mergeable', 'conflicting', or 'unknown' (GitHub hasn't finished computing
-- the merge commit yet — it does so asynchronously after each push).
ALTER TABLE tasks ADD COLUMN pr_mergeable TEXT NOT NULL DEFAULT '';

-- Cursor for merge-conflict feedback injection: the PR head SHA at which a
-- conflict was last surfaced to the agent. Mirrors last_failed_check_sha —
-- it keeps a conflict that persists across sweeps from re-injecting the same
-- feedback every interval. Cleared when the PR goes back to mergeable, so a
-- conflict re-introduced by a later base-branch move is surfaced again even
-- if the task's head commit hasn't changed.
ALTER TABLE task_pr_review_state ADD COLUMN last_conflict_sha TEXT;
