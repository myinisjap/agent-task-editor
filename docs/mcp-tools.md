# MCP Sidecar Tool Reference

The MCP (Model Context Protocol) sidecar is a small process (`mcp-server`) that runs alongside an agent subprocess, connected over stdio JSON-RPC. It exposes tools that let agents interact with the task system — signalling completion, requesting human input, persisting notes, and more.

## Which Providers Use It

| Provider | MCP Sidecar |
|---|---|
| `claude` | ✅ Yes (when `MCP_SERVER_PATH` is set) |
| `qwen_code` | ✅ Yes (when `MCP_SERVER_PATH` is set) |
| `anthropic` | ❌ No — uses native Go tool loop |
| `opencode` | ❌ No — opencode has no `--mcp-config` flag |
| `llm` | ❌ No — uses native Go tool loop |

## How It Works

1. The runner (ClaudeRunner / QwenRunner) calls `MCPManager.Prepare()` before starting the agent subprocess.
2. `Prepare()` writes a JSON config file that registers the `mcp-server` binary as an MCP server under the name `task-editor`.
3. The agent CLI receives `--mcp-config <tempfile>`, causing it to launch `mcp-server` as a child process.
4. The sidecar communicates over stdio using the MCP JSON-RPC protocol.
5. The sidecar reads `RUN_ID`, `RESULT_FILE`, and `TRANSITIONS` from its environment (set by `MCPManager.Prepare()`).
6. When `signal_complete` or `request_human` is called, the sidecar writes a result JSON file that the runner reads after the agent exits.

## Tool Reference

Tool names use the prefix `mcp__task-editor__` as injected by the CLI.

---

### `get_task_transitions`

Returns the available workflow transitions from the task's current label. Call this before `signal_complete` to know which outcome values are valid.

**Parameters:** none

**Returns:** JSON array of transition objects:
```json
[
  { "to_label": "testing", "path": "success" },
  { "to_label": "in-progress", "path": "failure" }
]
```

Returns `"No transitions configured for this label."` if the workflow has no outgoing agent transitions from the current label.

---

### `signal_complete`

Call when your work is done. The system resolves the correct next workflow label based on the outcome + available transitions.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `outcome` | `"success"` \| `"failure"` | ✅ | Whether the work succeeded or failed |
| `summary` | string | ✅ | Brief summary of what was done |

**Notes:**
- Always call `update_task_notes` before calling `signal_complete` to preserve context for subsequent agents.
- The sidecar also carries any notes/stored info accumulated during the run and flushes them alongside the result.
- If the agent exits with a non-zero code, the run is marked `failed` regardless of what was signalled.

---

### `request_human`

Pause the run and surface a message for human review. The task enters `waiting_human` status; no further agent processing occurs until a human approves or rejects.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `message` | string | ✅ | Question or context for the human reviewer |

**Behaviour:**
- The task emits a `task.needs_human` WebSocket event with the message.
- The run's status is set to `waiting_human`.
- A human clicks Approve or Reject in the UI; the task transitions accordingly and `active_agent_run_id` is cleared.
- The agent subprocess is **not** killed when `request_human` is called — the sidecar returns `"pausing for human input"` and the agent should then exit (or the runner waits for it to finish).

---

### `update_task_notes`

Write structured notes to the task so subsequent agents in the workflow have context. Notes are markdown and are injected at the top of the next agent's prompt under `"NOTES FROM PRIOR AGENT:"`.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `notes` | string | ✅ | Notes content (markdown supported) |
| `append` | boolean | — | If `true`, appends to existing notes rather than replacing. Default: `false` |

**Best practice:** Always call this before `signal_complete`. Use `append: true` if a prior agent's notes were present in your prompt (under "NOTES FROM PRIOR AGENT") to preserve them.

---

### `store_info`

Store structured information about this run that will be visible in the task view after completion. Replaces any previously stored info for this run.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `info` | string | ✅ | Information to store (markdown or plain text) |

**Use case:** Store a concise summary of what changed, what files were modified, or any notable decisions. This appears in the task detail view in the UI after the run completes.

---

## Environment Variables (set by MCPManager)

The `mcp-server` binary reads these from its environment:

| Variable | Description |
|---|---|
| `RUN_ID` | UUID of the current agent run |
| `RESULT_FILE` | Path to a temp file where the sidecar writes the JSON result |
| `TRANSITIONS` | JSON array of `{"to_label":"...","path":"..."}` objects for the current task |

## Building the MCP Server

```bash
cd backend
go build -o mcp-server ./cmd/mcp-server
```

Then set `MCP_SERVER_PATH=/path/to/mcp-server` in the server environment. When using `./dev.sh dev`, this is done automatically.
