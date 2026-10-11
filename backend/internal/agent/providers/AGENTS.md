# internal/agent/providers

Concrete agent backends (claude, anthropic, llm, opencode, qwen_code, codex_cli) and the MCP sidecar manager. Imports `agent` for the shared types; `agent` never imports this package (ADR 0017). Runners are constructed only in `cmd/server/main.go`'s `providerFactory`.

## Files

| File | Purpose |
|---|---|
| `claude.go` | `ClaudeRunner` only (`buildClaudeArgs`, `Run`, `runAttempt`, `shouldFallBackToColdStart`, `buildClaudeSettingsJSON`); `claude` CLI with stream-json output |
| `claude_credentials.go` | `ClaudeOAuthAccessToken` — reads `~/.claude/.credentials.json` and auto-refreshes the token when expired/expiring within 5 min (atomic write-back); returns "" when unrefreshable so the CLI uses its own flow |
| `claude_discovery.go` | Lists installed Claude plugins / user-level MCP servers for per-agent-config selection (`enabled_plugins`/`enabled_mcp_servers`); claude only |
| `claude_reset.go` | `parseClaudeResetTime` — turns "resets 6pm (TZ)" text into `ErrRateLimit.ResetAt`; blank-imports `time/tzdata` (prod image has no zoneinfo) |
| `claude_usage.go` | `FetchClaudeUsage`, `ErrNoClaudeCredentials` — Claude Max usage widget data for the dashboard |
| `anthropic.go`, `llm.go` | `AnthropicRunner`, `LLMRunner` — direct HTTP API runners. **Deprecated** (ADR 0018); `llm` also backs the retired `openai` alias |
| `qwen.go` | `QwenRunner` — Qwen Code CLI; reuses `MCPManager` and the claude stream-json envelope |
| `codex.go` | `CodexRunner` — `codex exec --json`; own event schema |
| `opencode.go` | `OpencodeRunner` — opencode CLI; own event schema; no MCP support (known gap) |
| `tools.go` | Tool implementations for `anthropic`/`llm` (read_file, write_file, run_bash, list_dir, search, str_replace, `CommandPolicy`, `runAccumulators`) |
| `mcp.go` | `MCPManager`, `SubtaskEnv` — prepares/cleans up the MCP sidecar config and result file (ADR 0004) |
| `pricing.go` | `estimateCostUSD(WithResolver)`, `PriceResolver` — manually maintained USD/1M-token table. Authoritative cost for `anthropic`/`llm`; *projection* source for the claude/qwen cost watchdog |
| `cost_watchdog.go` | `costWatchdog` — pure accumulator projecting total task cost from incremental usage; `claude.go`/`qwen.go` own cancelling the subprocess and building `agent.ErrCostBudgetExceeded` (ADR 0013) |
| `prompt.go` | `buildPrompt`, `buildResumePrompt`, `buildSystemPrompt`, `write*Section` helpers, `stripSourceCommentsMarker` — prompt assembly for every provider. Apply `stripSourceCommentsMarker` to every third-party string (ADR 0014) |
| `cli.go` | `allowlistEnv`, `mergeEnv`, `rawDump` (dev-only raw stdout capture via `AGENT_RAW_LOG_DIR`) — shared CLI-runner infrastructure |
| `parse.go` | Generic helpers: `runUsage`, `applyUsage`, `extractOutcome`, `is429Line`/`isTransientLine` (wrappers over `agent.ClassifyLine`) |
| `parse_streamjson.go` | Shared claude-style stream-json wire format owned by **no provider**: `classifyStreamJSON`, `classifyResultMessage`, `extractResultUsage`, `extractAssistantUsage`. Passes the raw line through as `Content` (display shaping is the frontend's `parseAgentLog.ts`). `Usage` is set for `result` (authoritative total) and `assistant` messages (per-turn increments, no cost — callers sum). `streamEvent.Parsed` scopes raw-line sniffing to unparsed lines |
| `parse_claude.go` | `isResumeErrorLine` and claude-specific result handling |
| `parse_qwen.go` | `classifyQwenJSON` — thin delegation to `classifyStreamJSON`; home for future qwen divergence |
| `parse_codex.go` | `classifyCodexJSON`, `classifyCodexItem` — codex JSONL schema; its `parsedJSON` return scopes sniffing like `Parsed` |
| `parse_opencode.go` | `classifyOpencodeJSON` — opencode NDJSON schema (no typed `Classification`); `parsedJSON` guards its raw sniff |

## Rules

- **One parser file per provider.** A runner calls only its own `parse_<name>.go`; never another provider's parser, and never `parse_streamjson.go` directly except `claude.go`. (ADR 0017)
- **Raw-line sniffing (`ClassifyLine`) is applied to stdout only for lines that did not parse as a structured event**; stderr is always sniffed. (ADR 0010)
- **Mid-run cost watchdog**: only `claude.go`/`qwen.go` wire it. Don't add it to a provider unless it has *priced, incremental* usage before the terminal event (codex prices at `turn.completed`; opencode's `step_finish` is a cumulative snapshot). (ADR 0013)
- Runtime mechanisms (worktrees, cancellation, retry, cost budgets, resume, review comments, locking, subtasks) are owned by `pool.go`/`dispatcher.go`/`worktree.go`/`subtasks.go`; see `../AGENTS.md`.

## Environment Variable Security (ADR 0019)

Agent subprocesses get an allow-listed env built by `allowlistEnv(<provider>EnvAllowlist)` in `cli.go`, never the full `os.Environ()`. `commonBaseEnvKeys` holds what every provider needs (`PATH`, `HOME`, locale/proxy/TLS trust, `GITHUB_TOKEN`/`GH_TOKEN`); each provider list adds its own.

> **If an agent needs a new env var, you MUST add it to the allowlist** (`commonBaseEnvKeys` if all providers need it, else the provider's list), or it silently won't reach the subprocess. Never add a real backend secret (`API_TOKEN`, `LLM_API_KEY`, `DATABASE_URL`, ...). Guards: `TestAllowlistEnv_EveryProviderHasGitHubToken`, `...HasPathAndHome`, `...NoProviderLeaksBackendSecrets`.

`mergeEnv` layers `input.AgentConfig.Env` (from the `ProviderConfig`) on top, blocking `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH` with a logged warning.

## Adding a New Provider

1. Implement `agent.Provider` here (plus `parse_<name>.go` if it has its own event format).
2. Add a case to `providerFactory` in `cmd/server/main.go`.
3. Add the string to `knownProviders` in `internal/api/handlers/agents.go`.
4. Declare capabilities in `frontend/src/lib/providerCapabilities.ts` and regenerate docs (root `AGENTS.md`).

**`anthropic` and `llm` are deprecated** (ADR 0018; see `docs/providers/anthropic.md`/`llm.md`). Don't remove them from `providerFactory` or `knownProviders` — existing configs must keep dispatching; `deprecatedProviders` in `handlers/agents.go` (checked only by `providers.go` Create/Update) rejects new ones. `providerFactory`'s `default` must keep returning `nil` (the dispatcher fails the run cleanly on nil); never let it resolve to a runner.

Providers read only the flat `input.AgentConfig.Provider/.Model/.Env`; the `ProviderConfig` split is resolved upstream (`../AGENTS.md`).

## Logging

stdlib `log/slog` only; see `../AGENTS.md` § Logging Conventions.
