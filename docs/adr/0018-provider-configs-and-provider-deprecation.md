# 18. Provider configs are a separate entity; `anthropic`/`llm` are deprecated, not removed

Status: Accepted

## Context

Agent configs (workflow dispatch) and chat sessions (interactive PTY) both need
a provider, model and env (API keys). Each owned its own copy. Separately, the
HTTP-API providers `anthropic` and `llm` are being retired, but existing
configs must keep working. The old `providerFactory` default silently turned
`llm`, `openai` and any unknown string into OpenAI-compatible calls.

## Decision

- Migration 039 splits `provider/model/env` into `provider_configs`, referenced
  by `provider_config_id` from `agent_configs` and `chat_sessions`. The
  migration backfilled one row per existing config reusing that row's id (both
  globally unique, so no positional-zip ambiguity), then rebuilt both tables.
  Delete is blocked with 409 while referenced.
- `agent.AgentConfig` keeps flat `Provider`/`Model`/`Env`.
  `dispatcher.go#toAgentConfig` fills them from the joined `ProviderConfig`;
  `handlers/chat.go` resolves the session's config for
  `TerminalManager.Attach`. Provider implementations never see the split.
- `anthropic` and `llm` stay registered in `providerFactory` and
  `knownProviders`, so existing configs dispatch. `deprecatedProviders`
  (checked only by `providers.go` Create/Update) rejects new configs and changes
  to a deprecated provider. `providerFactory`'s `default` returns `nil`, and
  `startRun` fails the run cleanly on a nil provider.

## Consequences

- Do not remove `anthropic`/`llm` from the factory or `knownProviders` to
  "finish" the deprecation, and do not restore a default runner.
- Adding a provider touches three places (factory, `knownProviders`, the
  runner file) plus capabilities (ADR 0007).
