# Provider: `codex_cli`

The `codex_cli` provider runs OpenAI's Codex CLI (`codex exec`) in non-interactive mode. Like `qwen_code`/`gemini_cli`, it supports the task-editor MCP sidecar, but Codex has its own JSON event schema and a fundamentally different, arguably stronger, command-execution safety model (a native sandbox + approval-mode system) rather than a simple allow/deny command-glob list — see below.

Everything in this document was verified against a live install of `@openai/codex` (npm, version `0.142.5`) — its `--help`/`codex exec --help` output, `codex mcp add`'s generated config, the upstream JSONL event schema (`codex-rs/exec/src/exec_events.rs`), and a real (unauthenticated) invocation — at the time this provider was added.

## Provider String

```
"provider": "codex_cli"
```

## How It Works

Runs:

```
codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox [--model <model>] "<prompt + system prompt>"
```

- `codex exec` is the dedicated non-interactive/scriptable subcommand (as opposed to bare `codex`, which launches the interactive TUI).
- `--json` emits one JSON object per line (JSONL) to stdout. **Codex interleaves plain-text diagnostic lines** (Rust `tracing` `ERROR ...` log lines) with the JSONL events on the same stream — the parser (`classifyCodexJSON`) falls back to a raw stdout log entry for any line that doesn't parse as JSON, rather than erroring.
- `--skip-git-repo-check` allows running inside a git worktree the CLI might not otherwise recognize as a "real" repo.
- `--dangerously-bypass-approvals-and-sandbox` skips **all** confirmation prompts and disables Codex's sandbox for the run. This is required for a headless run — without it, Codex pauses for interactive approval on every shell command the model wants to run. See "Command Allowlist / Denylist" below for the tradeoffs of this flag.
- There is no confirmed `--max-turns`-equivalent flag for `codex exec`, so no turn cap is passed.
- There is no separate `--system-prompt` flag; the system prompt is appended to the same trailing prompt argument as the task prompt (same treatment as `gemini_cli`).

## Credentials

The `codex` binary authenticates via **"Sign in with ChatGPT"** (`codex login`, interactively, once — writes `~/.codex/auth.json`) for Plus/Pro/Business/Edu/Enterprise ChatGPT plans, or the `OPENAI_API_KEY` environment variable for direct API billing. No server-side wiring beyond making sure the binary/credentials are available to the backend process (or container).

## MCP Tools

**All 5 MCP tools are supported** when `MCP_SERVER_PATH` is set, via a different wiring mechanism than `claude`/`qwen_code`:

| Tool | Description |
|---|---|
| `mcp__task-editor__get_task_transitions` | Returns available workflow transitions |
| `mcp__task-editor__signal_complete` | Marks the run done with `success` or `failure` |
| `mcp__task-editor__request_human` | Pauses the run for human input |
| `mcp__task-editor__update_task_notes` | Writes persistent notes for subsequent agents |
| `mcp__task-editor__store_info` | Stores a summary visible in the task UI |

Codex configures MCP servers via `[mcp_servers.<name>]` TOML sections in `$CODEX_HOME/config.toml` (there is also a `codex mcp add` CLI subcommand that writes the same format — used here only to confirm the exact shape, not invoked at runtime). Because that's a persistent config file rather than a per-invocation flag, this provider:

1. Creates a fresh, isolated temp directory per run.
2. Writes `<tempdir>/config.toml` containing a single `[mcp_servers.task-editor]` section (confirmed shape: `command`, `args`, and a nested `[mcp_servers.task-editor.env]` table for env vars).
3. Sets `CODEX_HOME=<tempdir>` only for that one subprocess invocation.
4. Removes the temp directory when the run ends.

This means concurrent runs never share or clobber a global `~/.codex/config.toml`, and no host-level Codex CLI configuration is touched.

See [mcp-tools.md](../mcp-tools.md) for full tool reference.

## Image Attachments

Not yet supported by this provider, even though `codex exec` itself has a documented `-i`/`--image <FILE>...` flag — wiring task attachments through to it is left for a future change.

## Command Allowlist / Denylist

**Neither `command_allowlist` nor `command_denylist` maps onto anything in this provider — both are unenforced.**

Codex has its own, arguably stronger, native safety system instead of a glob allow/deny list: a sandbox policy (`--sandbox read-only|workspace-write|danger-full-access`) and an approval policy (`--ask-for-approval untrusted|on-request|never`, plus the all-or-nothing `--dangerously-bypass-approvals-and-sandbox` this provider uses for headless operation). Because this provider must run fully unattended, it always passes `--dangerously-bypass-approvals-and-sandbox`, which **disables both the sandbox and the approval prompts** — Codex will execute any command the model proposes, unsandboxed, exactly like `qwen_code`'s `--approval-mode yolo` or `gemini_cli`'s `--yolo` do for their respective CLIs. If you need enforced command restrictions, prefer `claude` (denylist only), `anthropic`, or `llm`; there is currently no way to combine Codex's finer-grained sandbox/approval system with this codebase's per-run non-interactive requirement.

## Model Selection

Pass `model` in the agent config. It is passed via `--model <model>` to the CLI.

## Fallback Outcome Parsing

If the agent completes without calling `signal_complete`, the runner scans the final completed `agent_message` thread item's text (`{"type":"item.completed","item":{"type":"agent_message","text":...}}`) for `OUTCOME: success` / `OUTCOME: failure`, the same convention used by every other CLI provider. In-progress (`item.started`/`item.updated`) `agent_message` items are intentionally NOT scanned — only the terminal `item.completed` event is trusted, since intermediate deltas could contain a stale/partial `OUTCOME:` marker.

## Cost & Usage Reporting

The `turn.completed` event reports `usage.input_tokens` / `usage.output_tokens` (also `cached_input_tokens` and `reasoning_output_tokens`, not currently surfaced), which are used as-is. **No total-cost figure is reported by the Codex CLI's JSON output** — `cost_usd` is left at `0` for this provider rather than estimated. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Setup Checklist

1. Install the `codex` CLI (`npm i -g @openai/codex`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_CODEX_CLI` build arg)
2. Authenticate: run `codex login` to sign in with ChatGPT, or set `OPENAI_API_KEY`
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create an agent config with `"provider": "codex_cli"`
