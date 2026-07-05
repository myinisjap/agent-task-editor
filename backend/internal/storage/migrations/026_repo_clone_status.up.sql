-- Track the state of a repo's initial auto-clone so it can run asynchronously
-- (outside the HTTP request) without leaving the UI guessing.
--   clone_status: 'ready'   — repo is usable (local repos, and clones that finished)
--                 'cloning' — an async git clone is in progress
--                 'error'   — the clone failed; see clone_error
--   clone_error: human-readable failure detail when clone_status = 'error'
ALTER TABLE repos ADD COLUMN clone_status TEXT NOT NULL DEFAULT 'ready';
ALTER TABLE repos ADD COLUMN clone_error TEXT NOT NULL DEFAULT '';
