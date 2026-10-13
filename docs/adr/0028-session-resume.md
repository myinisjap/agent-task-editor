# 28. Only `claude` resumes sessions, with a condensed prompt

Status: Accepted

## Context

Reply-to-agent and feedback runs should continue the same conversation rather
than re-paying for the full task context.

## Decision

`claude` and `qwen_code` stream-json envelopes carry a `session_id`, and
`codex_cli` records a thread id; the pool persists it on any outcome
(`SetAgentRunSession`). Only `claude` resumes: `startRun` calls
`GetLatestTaskSession` when `provider == "claude" && resume_sessions` and sets
`RunInput.ResumeSessionID`; the runner passes `--resume` with a **condensed
prompt** (`buildResumePrompt`: human reply, feedback and open review comments
only). If the target is gone (`isResumeErrorLine`, or an error exit with no
stream output: `shouldFallBackToColdStart`) it retries once cold.
`DispatchReply` starts a run with `RunInput.HumanReply` (rendered as
`RESPONSE FROM HUMAN`) and shares `startRun` with `dispatch()`.
`agent_configs.resume_sessions` is a per-config opt-out.

## Consequences

- Prompt content that must reach a resumed run (e.g. untrusted source comments)
  has to be rendered in `buildResumePrompt` too.
