# cmd/mcp-server

A minimal MCP (Model Context Protocol) server that the `claude` provider runs as a sidecar subprocess. Communication is over stdio using JSON-RPC 2.0 (newline-delimited JSON).

## Purpose

The Claude CLI (`claude -p`) supports external MCP servers via `--mcp-config`. The sidecar exposes two tools that let the agent signal back to the orchestration system:

- `signal_complete(next_label, summary)` — marks the agent run as completed and specifies which workflow label to transition the task to
- `request_human(message)` — pauses the run and surfaces a message for human review before the agent continues

## How It Works

1. `ClaudeRunner.MCP.Prepare(runID)` writes a temporary MCP config JSON file pointing to this binary and sets `RESULT_FILE` to a temp path.
2. The sidecar is launched by the `claude` CLI automatically as it processes the `--mcp-config`.
3. When the agent calls `signal_complete` or `request_human`, the sidecar writes an `agent.Result` JSON to `RESULT_FILE`.
4. After the `claude` subprocess exits, `mcpCfg.ReadResult()` reads `RESULT_FILE` and returns the result to the pool.

## Environment Variables

| Variable | Description |
|---|---|
| `RUN_ID` | The agent run UUID (set by `MCPManager.Prepare`) |
| `RESULT_FILE` | Path where the result JSON is written (set by `MCPManager.Prepare`) |

## Protocol Details

- Only `initialize`, `tools/list`, and `tools/call` methods are implemented
- Notifications (no `id` field) are silently ignored — no response sent
- Unknown methods return JSON-RPC error `-32601`
- Scanner buffer is 4 MB to handle large tool call payloads
