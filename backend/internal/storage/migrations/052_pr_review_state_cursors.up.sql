-- Cursors that gate PR review/comment/check ingestion (see internal/ghsync's
-- ingestPRFeedback) on whether the PR has actually changed since the last
-- sweep, instead of unconditionally re-fetching reviews/comments/checks
-- every sweep for every task with an open PR (#340).

-- The PR's updated_at the last time a full reviews+comments fetch ran.
-- ingestPRFeedback only calls getReviews/getReviewComments when the live
-- head.UpdatedAt differs from this (or either is empty, which fails open to
-- "always fetch" for a forge that doesn't report updated_at).
ALTER TABLE task_pr_review_state ADD COLUMN last_pr_updated_at TEXT;

-- RFC3339 timestamp of the last FailedChecks fetch. Checks complete
-- asynchronously and do not bump the PR's updated_at, so they need their own
-- time floor (checkPollFloor) rather than being gated purely on
-- last_pr_updated_at, or a long-open PR with no further review activity
-- would stop having its checks polled at all.
ALTER TABLE task_pr_review_state ADD COLUMN last_checks_polled_at TEXT;
