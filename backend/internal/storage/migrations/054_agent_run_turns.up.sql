-- Actual internal turns a run consumed, for tuning agent_configs.max_turns
-- against reality. 0 means "not reported": only providers that expose a real
-- count set this (claude/qwen_code via the stream-json result event's
-- num_turns; anthropic/llm from their own agentic loop counter). codex_cli
-- and opencode report no comparable figure and leave it 0 — never estimated.
ALTER TABLE agent_runs ADD COLUMN turns_used INTEGER NOT NULL DEFAULT 0;
