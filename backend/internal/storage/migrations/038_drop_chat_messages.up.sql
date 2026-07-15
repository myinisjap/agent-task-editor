-- The interactive chat surface moved from a turn-based transcript to a live PTY
-- terminal (see agent/terminal.go). Terminal history is provided by the CLI's
-- own on-disk session store (via --resume) plus in-memory scrollback, so the
-- app-side chat_messages transcript table is no longer used. chat_sessions
-- stays: it still binds (repo, provider, worktree, provider_session_id).
DROP INDEX IF EXISTS idx_chat_messages_session;
DROP TABLE IF EXISTS chat_messages;
