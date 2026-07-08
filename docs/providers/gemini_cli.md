# Provider: `gemini_cli`

The `gemini_cli` provider runs Google's Gemini CLI (`gemini`) in headless mode. Like `qwen_code`, it supports the task-editor MCP sidecar (`signal_complete`/`request_human`/notes/etc.), but uses a different mechanism to wire it up and a different JSON event schema — see below.

Everything in this document was verified against a live install of `@google/gemini-cli` (npm, version `0.49.0`) — its `--help` output, its bundled source, and a real (unauthenticated) invocation — at the time this provider was added.

## Provider String

```
"provider": "gemini_cli"
```

## How It Works

Runs:

```
gemini -p "<prompt + system prompt>" --output-format stream-json --yolo [--skip-trust] [--model <model>]
```

- `-p`/`--prompt` puts the CLI in non-interactive (headless) mode. There is no separate `--system-prompt` flag (unlike `claude`/`qwen_code`), so the system prompt is appended to the same `-p` argument as the task prompt.
- `--output-format stream-json` emits one JSON object per line (NDJSON) to stdout, but with a **different event schema** from claude/qwen's stream-json envelope: `{"type":"init"|"message"|"tool_use"|"tool_result"|"result", ...}` rather than `{"type":"assistant"|"tool_use"|"tool_result"|"result", "message":{...}}`. This provider has its own `classifyGeminiJSON` parser rather than reusing `classifyStreamJSON`.
- `--yolo` auto-approves every tool call — required so the run doesn't hang waiting for interactive approval.
- `--skip-trust` is added only when MCP is configured. Without it, the CLI blocks MCP servers from loading with "this folder is untrusted", which would silently disable the sidecar tools in headless mode.
- There is no confirmed `--max-turns`-equivalent flag for the Gemini CLI's non-interactive mode, so no turn cap is passed.

## Credentials

The `gemini` binary authenticates via a Google account OAuth login (`gemini` interactively, once, to populate `~/.gemini/oauth_creds.json`) or the `GEMINI_API_KEY` / `GOOGLE_API_KEY` environment variables. No server-side wiring beyond making sure the binary/credentials are available to the backend process (or container).

## MCP Tools

**All 5 MCP tools are supported** when `MCP_SERVER_PATH` is set, via a different wiring mechanism than `claude`/`qwen_code`:

| Tool | Description |
|---|---|
| `mcp__task-editor__get_task_transitions` | Returns available workflow transitions |
| `mcp__task-editor__signal_complete` | Marks the run done with `success` or `failure` |
| `mcp__task-editor__request_human` | Pauses the run for human input |
| `mcp__task-editor__update_task_notes` | Writes persistent notes for subsequent agents |
| `mcp__task-editor__store_info` | Stores a summary visible in the task UI |

Unlike `claude`/`qwen_code`, the Gemini CLI does not accept a per-invocation `--mcp-config <file>` flag. Instead it reads `{"mcpServers": {...}}` from a `settings.json` file under its "home" directory (`$GEMINI_CLI_HOME/.gemini/settings.json`, or `~/.gemini/settings.json` if `GEMINI_CLI_HOME` is unset — confirmed by reading the installed CLI's bundled source). Because that's a persistent config location rather than a per-run flag, this provider:

1. Creates a fresh, isolated temp directory per run.
2. Writes `<tempdir>/.gemini/settings.json` containing the task-editor MCP server entry (same `command`/`args`/`env` shape the sidecar uses everywhere else).
3. Sets `GEMINI_CLI_HOME=<tempdir>` only for that one subprocess invocation.
4. Removes the temp directory when the run ends.

This means concurrent runs never share or clobber a global `~/.gemini/settings.json`, and no host-level Gemini CLI configuration is touched.

See [mcp-tools.md](../mcp-tools.md) for full tool reference.

## Image Attachments

Not yet supported. Reserved for if/when the `gemini` CLI's non-interactive mode gains a documented image-attachment flag.

## Command Allowlist / Denylist

**Neither `command_allowlist` nor `command_denylist` is enforced for this provider.** There is no confirmed Gemini CLI flag equivalent to claude's `--allowedTools`/`--disallowedTools` or a native command-glob restriction system in non-interactive mode. If you need allowlist/denylist enforcement, prefer `claude` (denylist only), `anthropic`, or `llm`.

## Model Selection

Pass `model` in the agent config. It is passed via `--model <model>` to the CLI.

## Fallback Outcome Parsing

If the agent completes without calling `signal_complete`, the runner scans the final assistant message text (the terminal `{"type":"message","role":"assistant",...}` event) for `OUTCOME: success` / `OUTCOME: failure`, the same convention used by every other CLI provider.

## Cost & Usage Reporting

The terminal `{"type":"result", "stats": {"input_tokens":..., "output_tokens":...}}` event reports token counts, which are used as-is. **No total-cost figure is reported by the Gemini CLI's JSON output** — `cost_usd` is left at `0` for this provider rather than estimated. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Setup Checklist

1. Install the `gemini` CLI (`npm i -g @google/gemini-cli`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_GEMINI_CLI` build arg)
2. Authenticate: run `gemini` once interactively to log in with a Google account, or set `GEMINI_API_KEY`/`GOOGLE_API_KEY`
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create an agent config with `"provider": "gemini_cli"`
