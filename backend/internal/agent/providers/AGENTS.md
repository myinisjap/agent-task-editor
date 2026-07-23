# internal/agent/providers

The `providers` package implements the concrete agent backends (claude, anthropic, llm, opencode, qwen_code, gemini_cli, codex_cli) and the MCP sidecar manager. It imports the sibling `agent` package for the shared runtime types (`Provider`, `RunInput`, `Result`, `LogEntry`, `LogEntryType`, `Classification`, `ClassifyLine`, `ErrRateLimit`, `ErrTransient`, `BackoffDuration`, `TransitionHint`, `ReviewComment`, `AgentConfig`, `Task`) — the dependency is one-directional: `agent` never imports `providers`. Concrete runners (`ClaudeRunner`, `AnthropicRunner`, `LLMRunner`, `QwenRunner`, `GeminiRunner`, `CodexRunner`, `OpencodeRunner`) are constructed only in `backend/cmd/server/main.go`'s `providerFactory`.

## Files

| File | Purpose |
|---|---|
| `claude.go` | `ClaudeRunner` only — `binary`, `buildClaudeArgs`, `Run`, `runAttempt`, `shouldFallBackToColdStart`, `buildClaudeSettingsJSON`. Runs `claude` CLI subprocess with stream-json output |
| `claude_credentials.go` | `ClaudeOAuthAccessToken` — reads the OAuth access token from `~/.claude/.credentials.json` and **auto-refreshes it** (Anthropic OAuth token endpoint + stored refresh token) when expired/expiring within 5 min, persisting rotated tokens back to the file atomically; used by the claude subprocess env injection and the dashboard usage widget. Returns "" when expired-and-unrefreshable so the CLI falls back to its own refresh flow |
| `claude_discovery.go` | `ListInstalledClaudePlugins`/`ListAvailableClaudeMCPServers` — discovers Claude plugins (`~/.claude/plugins/installed_plugins.json`) and user-level MCP servers (`~/.claude.json`'s global `mcpServers`) installed/configured on the machine, for per-agent-config selection (`enabled_plugins`/`enabled_mcp_servers`); `claude`-provider only |
| `claude_reset.go` | `parseClaudeResetTime` — parses Claude's "resets 6pm (America/Chicago)"-style session/usage-limit text into an exact `agent.ErrRateLimit.ResetAt` (+1min retry buffer), so the pool can schedule an exact retry instead of falling back to `BlockWithBackoff`. Blank-imports `time/tzdata` (embeds IANA tzdata into the binary — the prod container has no `/usr/share/zoneinfo`) |
| `claude_usage.go` | `FetchClaudeUsage`, `ErrNoClaudeCredentials` — fetches the Claude Max usage widget data (5-hour/weekly percentages) from Anthropic's OAuth usage endpoint; used by the dashboard |
| `anthropic.go` | `AnthropicRunner` — calls the Anthropic Messages API directly |
| `llm.go` | `LLMRunner` — calls any OpenAI-compatible API |
| `qwen.go` | `QwenRunner` — runs the Qwen Code CLI; reuses `MCPManager` and the claude stream-json envelope via `parse_qwen.go` |
| `gemini.go` | `GeminiRunner` — runs the Gemini CLI; own event schema, parsed via `parse_gemini.go` |
| `codex.go` | `CodexRunner` — runs the Codex CLI (`codex exec --json`); own event schema, parsed via `parse_codex.go` |
| `opencode.go` | `OpencodeRunner` — runs the opencode CLI; own event schema, parsed via `parse_opencode.go`. No MCP support (`ponytail`-marked known gap) |
| `tools.go` | Shared tool implementations for `anthropic` and `llm` providers (read_file, write_file, run_bash, list_dir, search, str_replace, `CommandPolicy`, `runAccumulators`) |
| `mcp.go` | `MCPManager`, `SubtaskEnv` — prepares/cleans up the MCP sidecar config and result file |
| `pricing.go` | `estimateCostUSD` — manually maintained USD-per-1M-token pricing table used by the `anthropic`/`llm` providers (not the `claude` CLI provider, which reports its own authoritative cost) |
| `prompt.go` | `buildPrompt`, `buildResumePrompt`, `buildSystemPrompt`, `writeHumanReplySection`, `writeSubtaskConflictSection`, `writeFeedbackSection`, `writeReviewCommentsSection` — prompt assembly shared by every provider |
| `cli.go` | `mergeEnv`, `rawDump`/`openRawDump`/`WriteLine`/`Close` — infrastructure shared by every CLI-subprocess runner (env sanitization, dev-only raw stdout capture gated by `AGENT_RAW_LOG_DIR`) |
| `parse.go` | Generic parser helpers shared across providers: `runUsage`, `applyUsage`, `extractOutcome`, `is429Line`/`isTransientLine` (thin wrappers over `agent.ClassifyLine`) |
| `parse_streamjson.go` | Wire-format helpers for the claude-style stream-json format: `classifyStreamJSON`, `classifyResultMessage`, `extractResultUsage`, `assistantHasToolUse`. Classifies each line's `LogType` and passes the **raw line** through as `Content` — display shaping (extracting text, summarizing tool calls) is the frontend's job (`parseAgentLog.ts`). This is a format library owned by **no provider** — nothing provider-specific lives here |
| `parse_claude.go` | Claude's parse entry point: `isResumeErrorLine` and claude-specific result handling. `claude.go` calls `classifyStreamJSON` directly (stream-json is claude's native format) |
| `parse_qwen.go` | `classifyQwenJSON` — qwen's parse entry point, a thin delegation to `parse_streamjson.go`'s `classifyStreamJSON` (qwen reuses claude's exact envelope today). `qwen.go` calls this, never `classifyStreamJSON` directly. Intentionally near-trivial — the home for future qwen-specific parsing divergence |
| `parse_gemini.go` | `classifyGeminiJSON` + its envelope types — gemini's own event schema (`{"type":"init"\|"message"\|"tool_use"\|"tool_result"\|"result",...}`), incompatible with claude/qwen's stream-json |
| `parse_codex.go` | `classifyCodexJSON`, `classifyCodexItem` + envelope types (`codexItemEnvelope`) — codex's own JSONL event schema (`{"type":"thread.started"\|"turn.started"\|"turn.completed"\|"turn.failed"\|"item.started"\|"item.updated"\|"item.completed"\|"error",...}`) |
| `parse_opencode.go` | `classifyOpencodeJSON` — opencode's own NDJSON event schema (text/tool_use/tool_result/step_finish/step_start) |

### One parser file per provider

Each provider's parsing logic lives in exactly one `parse_<name>.go` file — no file may contain parsing logic for more than one provider. `parse_streamjson.go` is the sole exception: it is shared wire-format code owned by no provider, reached only through a provider's own parser (`claude.go` calling `classifyStreamJSON` directly since that *is* claude's format; `parse_qwen.go`'s `classifyQwenJSON` delegating to it since qwen reuses claude's envelope). A runner file (`claude.go`, `qwen.go`, `gemini.go`, `codex.go`, `opencode.go`) calls only its own `parse_<name>.go` entry point — never another provider's parser and never `parse_streamjson.go` directly (except `claude.go`, per above). This layout exists so future per-provider parsing changes land in one file each, without touching siblings.

## Branch-per-task / Worktrees, Run Cancellation, Retry Policy, Session Resume, Review Comments, Rework-Loop, Dispatch Locking, Subtask Coordinator

These runtime mechanisms are documented in `../AGENTS.md` (the core `agent` package) — they're owned by `pool.go`/`dispatcher.go`/`worktree.go`/`subtasks.go`, not by this package. This package only supplies the `Provider` implementations those mechanisms drive.

## Environment Variable Security

`mergeEnv` (`cli.go`) blocks keys that could hijack the subprocess: `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`. Blocked keys are logged as warnings, not silently dropped. `input.AgentConfig.Env` (the map `mergeEnv` merges in) comes from the referenced `ProviderConfig`'s `env` JSON column rather than directly off `agent_configs` — `mergeEnv` itself is unaffected by that split, it only ever sees the already-resolved map.

## Adding a New Provider

1. Implement `agent.Provider` in a new file here (e.g. `providers/newthing.go`); if it needs its own event-parsing logic, give it a dedicated `parse_newthing.go` per the one-parser-per-provider rule above.
2. Add a new case to `providerFactory` in `cmd/server/main.go`.
3. Add the provider string to `knownProviders` in `internal/api/handlers/agents.go` (validated on both agent-config and provider-config create/update — see `internal/api/handlers/providers.go`).

`AgentConfig.Provider`/`.Model`/`.Env` (the fields providers actually read off `input.AgentConfig`) are populated from the joined `ProviderConfig` — see `../AGENTS.md` § Adding a New Provider and [docs/agents.md § Provider Configs](../../../../docs/agents.md#provider-configs) for the full resolution path. A new provider implementation never needs to know about that split; it only ever reads `Provider`/`.Model`/`.Env` off `input.AgentConfig` the same way every other provider does.

## Logging Conventions

Same as the core `agent` package (stdlib `log/slog`, no third-party logging libraries) — see `../AGENTS.md` § Logging Conventions.
