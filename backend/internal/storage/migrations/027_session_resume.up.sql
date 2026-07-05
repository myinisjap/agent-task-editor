-- Session continuity across runs (claude provider). session_id records the
-- provider CLI session for a run (from the stream-json envelope) so a later
-- run on the same task can resume it with full prior context instead of
-- starting cold. Empty for providers/runs without a session.
ALTER TABLE agent_runs ADD COLUMN session_id TEXT NOT NULL DEFAULT '';

-- Per-agent-config opt-out for session resume (on by default). A config that
-- wants fresh eyes every run (e.g. an agent-review stage that shouldn't share
-- the implementer's session) sets this to 0.
ALTER TABLE agent_configs ADD COLUMN resume_sessions INTEGER NOT NULL DEFAULT 1;
