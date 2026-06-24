# Agents

## Agent Configs

An agent config connects a set of workflow labels to a specific AI provider. The dispatcher matches tasks to configs by label name.

### Fields

| Field | Description |
|---|---|
| `name` | Human-readable name |
| `provider` | `claude`, `anthropic`, or `llm` |
| `model` | Model identifier (e.g. `claude-sonnet-4-5`, `gpt-4o`) |
| `labels` | JSON array of label names this agent handles (e.g. `["todo","in-progress"]`) |
| `system_prompt` | Custom system instructions; appended with MCP tool guidance automatically |
| `max_tokens` | Maximum tokens per response (0 = provider default) |
| `timeout_secs` | Maximum run duration in seconds (0 = 600s default) |
| `env` | JSON object of additional environment variables for the agent process |

## Providers

### `claude` — Claude CLI subprocess

Runs the `claude` CLI in headless mode using `claude -p <prompt> --output-format stream-json`. The CLI must be installed and authenticated on the host (or container). Auth is shared via the `~/.claude` directory.

**MCP sidecar:** When `MCP_SERVER_PATH` is set, the runner launches the `mcp-server` sidecar alongside the `claude` process. The sidecar exposes two tools over stdio JSON-RPC:

- `signal_complete(next_label, summary)` — tells the system the agent is done and which label to move to
- `request_human(message)` — pauses the run and surfaces a message for human review

Without MCP, the run always completes with status `completed` (no label transition is attempted).

**Allowed tools:** `Edit, Write, Read, Bash, Glob, Grep` plus the two MCP tools when configured.

**Environment variable security:** The `env` field in agent configs passes additional env vars to the subprocess. Keys that could hijack process execution (`PATH`, `LD_PRELOAD`, `HOME`, `SHELL`, etc.) are blocked and logged as warnings.

### `anthropic` — Anthropic Messages API

Calls the Anthropic Messages API directly — no CLI binary needed. Requires `LLM_API_KEY` to be set to an Anthropic API key. Billed per-token (separate from Claude Max subscriptions).

Built-in tools: `read_file`, `write_file`, `run_bash`, `signal_complete`, `request_human`. The runner handles tool use natively in a multi-turn loop.

Use this provider when you want direct API access without the Claude CLI overhead, or when running in environments where the CLI isn't available.

### `llm` — OpenAI-compatible API

Calls any OpenAI-compatible endpoint (OpenAI, Azure OpenAI, Ollama, LM Studio, etc.) at `LLM_BASE_URL`. Requires `LLM_API_KEY`.

Same built-in tool set as the `anthropic` provider.

## Dispatcher

The dispatcher runs a background goroutine that sweeps the database every 5 seconds:

1. Queries `ListAgentPickupTasks` — tasks whose label appears in any agent-triggerable transition AND whose `active_agent_run_id IS NULL`.
2. Loads all agent configs, matches each task to the first config whose `labels` array contains the task's label.
3. Creates an `agent_runs` record with status `pending`.
4. Sets the task's `active_agent_run_id` (and updates `current_agent_run_id`) — this prevents the next sweep from double-dispatching.
5. Submits a `Job` to the worker pool. If the pool queue is full, marks the run `failed` and clears `active_agent_run_id`.

## Worker Pool

The pool manages `MAX_WORKERS` concurrent goroutines (default 5). Each worker:

1. Sets the run status to `running`.
2. Calls `Provider.Run()` which streams `LogEntry` values on a channel.
3. Persists log entries to `agent_logs` in batched transactions (flush every 500ms or every 50 entries).
4. Simultaneously publishes each entry to the WebSocket hub for live streaming.
5. On completion, sets the run status to `completed`, `failed`, or `waiting_human`.
6. For `completed`/`failed`, clears `active_agent_run_id` so the dispatcher can re-pick-up the task.
7. For `waiting_human`, leaves `active_agent_run_id` set — dispatch is blocked until a human approves or rejects.

## Run Lifecycle

```
created (pending)
    │
    ▼ worker picks up
running
    │
    ├─▶ completed  → engine.Transition(nextLabel)  → active_run cleared
    ├─▶ failed                                      → active_run cleared
    └─▶ waiting_human → task.needs_human WS event  → active_run stays set
                            │
                    human approves/rejects
                            │
                    engine.Transition()              → active_run cleared
```

## Log Entry Types

| Type | Meaning |
|---|---|
| `system` | Internal messages (process start, timeout, exit) |
| `stdout` | Agent text output / parsed assistant messages |
| `stderr` | Stderr from the subprocess |
| `tool_call` | Tool invocation (Edit, Bash, signal_complete, etc.) |
| `tool_result` | Result returned to the agent after a tool call |

## Prompt Construction (ClaudeRunner)

The user prompt sent to the agent is assembled as:

```
[FEEDBACK FROM PRIOR REVIEW:
<feedback from the previous run, if any>

---]

[IMPLEMENTATION PLAN:
<prior plan content, if any>

---]

Task: <title>

<description>
```

The system prompt is the agent config's `system_prompt` field, with this suffix always appended:

> When your work is complete, call the signal_complete tool with the next workflow label and a summary. If you need human input before continuing, call request_human.
