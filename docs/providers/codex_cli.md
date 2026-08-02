# Provider: `codex_cli`

The `codex_cli` provider runs OpenAI's Codex CLI (`codex exec`) in non-interactive mode. Like `qwen_code`, it supports the task-editor MCP sidecar, but Codex has its own JSON event schema and a fundamentally different, arguably stronger, command-execution safety model (a native sandbox + approval-mode system) rather than a simple allow/deny command-glob list — see below.

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
- There is no separate `--system-prompt` flag; the system prompt is appended to the same trailing prompt argument as the task prompt.

## Credentials

The `codex` binary authenticates via **"Sign in with ChatGPT"** (`codex login`, interactively, once — writes `~/.codex/auth.json`) for Plus/Pro/Business/Edu/Enterprise ChatGPT plans, or the `OPENAI_API_KEY` environment variable for direct API billing. No server-side wiring beyond making sure the binary/credentials are available to the backend process (or container).

**Subprocess environment:** the `codex` subprocess does not inherit the
backend's full environment (see [providers/README.md § Subprocess
environment](README.md#subprocess-environment)). It receives `PATH`/`HOME`
plus `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_ORG`/`OPENAI_ORGANIZATION`,
`CODEX_HOME` (also set explicitly per-run — see below) if set on the
backend, plus a handful of locale/proxy/TLS vars, plus this agent config's
own `env` field. Any other backend env var will not reach the CLI.

## MCP Tools

**All 6 MCP tools are supported** (7 with `create_subtask`, which is exposed only when the agent config enables subtasks) when `MCP_SERVER_PATH` is set, via a different wiring mechanism than `claude`/`qwen_code`:

| Tool | Description |
|---|---|
| `mcp__task-editor__get_task_transitions` | Returns available workflow transitions |
| `mcp__task-editor__signal_complete` | Marks the run done with `success` or `failure` |
| `mcp__task-editor__request_human` | Pauses the run for human input |
| `mcp__task-editor__update_task_notes` | Writes persistent notes for subsequent agents |
| `mcp__task-editor__store_info` | Stores a summary visible in the task UI |
| `mcp__task-editor__resolve_comment` | Marks an open inline review comment as addressed |
| `mcp__task-editor__create_subtask` | Splits the task into a child task (only exposed when the agent config has `subtasks_enabled`) |

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

Codex has its own, arguably stronger, native safety system instead of a glob allow/deny list: a sandbox policy (`--sandbox read-only|workspace-write|danger-full-access`) and an approval policy (`--ask-for-approval untrusted|on-request|never`, plus the all-or-nothing `--dangerously-bypass-approvals-and-sandbox` this provider uses for headless operation). Because this provider must run fully unattended, it always passes `--dangerously-bypass-approvals-and-sandbox`, which **disables both the sandbox and the approval prompts** — Codex will execute any command the model proposes, unsandboxed, exactly like `qwen_code`'s `--approval-mode yolo` does for its CLI. If you need enforced command restrictions, prefer `claude` (denylist only), `anthropic`, or `llm`; there is currently no way to combine Codex's finer-grained sandbox/approval system with this codebase's per-run non-interactive requirement.

## Session Resume

The `thread.started` event carries a `thread_id`, which the runner records. When a later run on the same task hits this same agent config (and the config's `resume_sessions` flag is on — the default), the runner invokes `codex exec ... resume <thread_id> "<prompt>"` (Codex 0.145.0's `codex exec resume` subcommand), so prior context carries forward instead of starting cold. The runner's flag-before-subcommand argument order (flags, then `resume <id>`, then the prompt) parses correctly against a live CLI — confirmed by observing it reach the resume lookup and fail only on a nonexistent thread id, not on argument parsing.

## Model Selection

Pass `model` on the referenced [Provider Config](../agents.md#provider-configs). It is passed via `--model <model>` to the CLI.

## Fallback Outcome Parsing

If the agent completes without calling `signal_complete`, the runner scans the final completed `agent_message` thread item's text (`{"type":"item.completed","item":{"type":"agent_message","text":...}}`) for `OUTCOME: success` / `OUTCOME: failure`, the same convention used by every other CLI provider. In-progress (`item.started`/`item.updated`) `agent_message` items are intentionally NOT scanned — only the terminal `item.completed` event is trusted, since intermediate deltas could contain a stale/partial `OUTCOME:` marker.

## Cost & Usage Reporting

The `turn.completed` event reports `usage.input_tokens` / `usage.output_tokens` (also `cached_input_tokens` and `reasoning_output_tokens`, not currently surfaced). **No total-cost figure is reported by the Codex CLI's JSON output**, so these tokens are priced by `CodexRunner.Run` against the same pricing table the `anthropic`/`llm`/`qwen_code` providers use (`applyUsageWithCost`, DB-backed via Configuration → Pricing, with a hardcoded fallback table — see `providers/pricing.go`). This produces an *estimated*, not authoritative, `cost_usd`. When the configured model isn't in the pricing table, `cost_usd` stays `0` but the run is flagged `cost_unknown` so the UI shows "cost unknown" instead of a misleading `$0`. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

**Pre-dispatch `max_cost_usd` guard: supported**, for models in the pricing table — the task's accumulated estimated spend across `codex_cli` runs now blocks the next dispatch once the budget is exhausted, the same as `anthropic`/`llm`/`qwen_code`. If any run's cost is unknown (unpriced model), the accumulated spend can't be trusted; see [agents.md § Cost Budgets](../agents.md#cost-budgets) for how that's handled.

**Mid-run cost kill switch: not supported.** Codex's usage is only known once a full `turn.completed` event arrives at the end of a run, not incrementally, so a mid-run cost projection isn't possible for this provider. Only the pre-dispatch guard above applies.

## Setup Checklist

1. Install the `codex` CLI (`npm i -g @openai/codex`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_CODEX_CLI` build arg)
2. Authenticate: run `codex login` to sign in with ChatGPT, or set `OPENAI_API_KEY`
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "codex_cli"`, then an agent config referencing it via `provider_config_id`
