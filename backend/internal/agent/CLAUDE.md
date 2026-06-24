# internal/agent

The agent package owns everything to do with running AI agents: the provider abstraction, three concrete providers, the worker pool, and the dispatcher.

## Files

| File | Purpose |
|---|---|
| `provider.go` | `Provider` interface, `RunInput`, `Result`, `LogEntry`, `LogEntryType` |
| `claude.go` | `ClaudeRunner` — runs `claude` CLI subprocess with stream-json output |
| `anthropic.go` | `AnthropicRunner` — calls Anthropic Messages API directly |
| `llm.go` | `LLMRunner` — calls any OpenAI-compatible API |
| `tools.go` | Shared tool implementations for `anthropic` and `llm` providers (read_file, write_file, run_bash) |
| `mcp.go` | `MCPManager` — prepares/cleans up the MCP sidecar config and result file |
| `pool.go` | `Pool` — bounded goroutine pool; persists logs, publishes WS events |
| `dispatcher.go` | `Dispatcher` — periodic DB sweep; matches tasks to configs; submits jobs |

## Provider Interface

```go
type Provider interface {
    Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error)
}
```

Providers stream log entries on `logCh` as they run. The pool drains this channel, persists entries in batches, and publishes them to the WS hub.

## Result Status Values

- `completed` — agent finished; `NextLabel` optionally specifies where to move the task
- `failed` — something went wrong; task stays on current label (re-dispatch will retry)
- `waiting_human` — agent called `request_human`; `Message` surfaces to the UI

## Dispatch / Active Run Locking

`active_agent_run_id` prevents double-dispatch:
- Dispatcher sets it when creating a run
- Pool clears it on `completed` / `failed`
- Pool leaves it set on `waiting_human`
- `UpdateTaskLabel` (any workflow transition) always clears it via SQL

## Environment Variable Security

`mergeEnv` (in `claude.go`) blocks keys that could hijack the subprocess: `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`. Blocked keys are logged as warnings, not silently dropped.

## Adding a New Provider

1. Implement `Provider` in a new file (e.g. `gemini.go`)
2. Add a new case to `providerFactory` in `cmd/server/main.go`
3. Add the provider string to the `AgentConfig.Provider` validation if any
