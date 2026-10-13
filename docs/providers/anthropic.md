# Provider: `anthropic` (removed)

> **Removed in v0.17.0.** The `anthropic` provider no longer exists. Creating or switching a provider config to it is
> rejected with `400`. Existing provider/agent configs that still reference it are kept in the database (nothing
> is deleted), but any run for such a config fails at dispatch: the task escalates to `waiting_human` with
> "provider anthropic was removed in v0.17.0; switch this agent config to claude/codex_cli/opencode/qwen_code", the Health
> page shows a `removed_provider_anthropic` error, and the provider dropdown flags the config as removed.
>
> To migrate, open the config on the Providers page and switch it to [`claude`](claude.md),
> [`codex_cli`](codex_cli.md), [`opencode`](opencode.md) or [`qwen_code`](qwen_code.md), then reply to the
> escalated task to resume it. `LLM_API_KEY` and `LLM_BASE_URL` are no longer read by the backend.
