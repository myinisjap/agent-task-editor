-- Interactive chat sessions: free-form conversations against a repo, separate
-- from the task/workflow state machine. Each session runs one CLI turn per
-- user message in its own git worktree, resuming the provider session between
-- turns (all providers support resume; see docs/providers/*).
CREATE TABLE chat_sessions (
    id                  TEXT PRIMARY KEY,
    repo_id             TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    provider            TEXT NOT NULL,
    model               TEXT NOT NULL DEFAULT '',
    title               TEXT NOT NULL DEFAULT '',
    -- Provider-side conversation id (claude/qwen/gemini/codex/opencode session).
    -- Empty until the first turn completes; passed back to resume the next turn.
    provider_session_id TEXT NOT NULL DEFAULT '',
    -- Git worktree provisioned on first turn; removed when the session is deleted.
    worktree_path       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_chat_sessions_repo ON chat_sessions(repo_id);

-- One row per streamed log entry / message. Mirrors agent_logs' Type vocabulary
-- (stdout/stderr/system/tool_call/tool_result) plus 'user' for human messages,
-- so the transcript replays coherently in the UI and survives reload.
CREATE TABLE chat_messages (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_chat_messages_session ON chat_messages(session_id, created_at);
