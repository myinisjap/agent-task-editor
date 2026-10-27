// Single source of truth for what each agent provider supports.
//
// This is read by AgentConfigForm, ProviderConfigForm, and
// CommandFilterEditor to surface capability gaps inline, at config time,
// instead of letting a user discover them only when a run silently
// under-delivers (e.g. picking `opencode` and finding tasks never
// transition, or a cost budget on a provider that doesn't track cost).
//
// docs/agents.md's "Capability Matrix" table is generated from this file —
// see scripts/gen-capability-docs.mjs (`npm run gen:capability-docs`).
// Keep the two in sync: edit here, then regenerate the doc. Do NOT hand-edit
// the table between the `<!-- BEGIN capability-matrix (generated) -->` /
// `<!-- END capability-matrix (generated) -->` markers in docs/agents.md.

export type Capability =
  | 'taskEditorTools'
  | 'labelTransitions'
  | 'mcpServers'
  | 'commandAllowlist'
  | 'commandDenylist'
  | 'costTracking'
  | 'costWatchdog'
  | 'imageAttachments'
  | 'maxTurns'
  | 'sessionResume'
  | 'subtasks'
  | 'effort'
  | 'permissionMode'

export type Support = 'full' | 'partial' | 'none'

export interface CapabilityEntry {
  support: Support
  /** Short human-readable explanation, surfaced verbatim in the UI. */
  note?: string
}

export type ProviderCapabilities = Partial<Record<Capability, CapabilityEntry>>

// Ordered list of known providers, matching docs/agents.md's Capability
// Matrix column order. `agentTemplates.ts`'s PROVIDERS list is the
// UI-selectable dropdown.
export const KNOWN_PROVIDERS = ['claude', 'qwen_code', 'codex_cli', 'opencode'] as const

// Providers that were removed (v0.17.0). The backend rejects them for new or
// updated configs and fails any run of a legacy config that still references
// one; the UI flags such configs so they can be switched. "openai" was a dead
// dropdown alias of `llm`. See docs/providers/README.md.
export const REMOVED_PROVIDERS = new Set(['anthropic', 'llm', 'openai'])

export const PROVIDER_CAPABILITIES: Record<string, ProviderCapabilities> = {
  claude: {
    taskEditorTools: { support: 'full', note: 'All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled).' },
    labelTransitions: { support: 'full' },
    mcpServers: { support: 'full', note: 'Supports Claude plugins and user-level MCP servers.' },
    commandAllowlist: {
      support: 'partial',
      note: 'Not an effective restriction for the claude provider: the CLI only auto-approves matches, it does not block non-matching commands. Use the denylist instead.',
    },
    commandDenylist: { support: 'full' },
    costTracking: { support: 'full', note: 'Authoritative cost and token counts.' },
    costWatchdog: {
      support: 'full',
      note: 'Mid-run kill switch: projects cost from incremental assistant-message token usage via the pricing table and cancels the run if it crosses the effective budget, escalating to waiting_human. The projection is an estimate (not the CLI\'s own authoritative total_cost_usd, which is only known after the run ends) — under a subscription plan with $0 real marginal cost, this estimate can still be nonzero and trigger a kill.',
    },
    imageAttachments: {
      support: 'none',
      note: 'The claude CLI has no --image flag (verified against v2.1.220), so this provider does not attempt to pass one. The dispatcher still copies attachments into the worktree under .task_attachments/, listed in the prompt, so agents can read them as files via the Read tool.',
    },
    maxTurns: { support: 'full', note: 'Enforced via --max-turns. Hitting the cap escalates the run to waiting_human instead of retrying.' },
    sessionResume: { support: 'full', note: 'session_id + --resume.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
    effort: {
      support: 'full',
      note: 'Passed as --effort. Supports low/medium/high/xhigh/max. Not all models support effort levels, and higher levels may be restricted by your Anthropic organization — either case degrades silently (the CLI only warns), so verify against the agent run logs.',
    },
    permissionMode: {
      support: 'full',
      note: 'Passed as --permission-mode. Leave as Default (unset) to keep today\'s behavior (no flag — the CLI\'s own "auto" classifier). command_denylist is still enforced even under bypassPermissions (verified empirically).',
    },
  },
  qwen_code: {
    taskEditorTools: { support: 'full', note: 'All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled).' },
    labelTransitions: { support: 'full' },
    mcpServers: { support: 'none' },
    commandAllowlist: {
      support: 'none',
      note: "Not enforced for the qwen_code provider: qwen's --allowed-tools only bypasses confirmation, and the runner always passes --approval-mode yolo (auto-approve all tools), so allowlist entries have no effect. Use the denylist instead.",
    },
    commandDenylist: {
      support: 'partial',
      note: "Enforced via qwen's --exclude-tools flag (folds into its permissionsDeny policy), which is honored even under yolo mode. Verified against the 0.25.2 bundle: qwen's shell tool is named run_shell_command (not Bash), so each pattern is sent as run_shell_command(pattern) — a Bash(pattern) entry, as used for --allowed-tools above, would be a silent no-op here. qwen's own matching is exact-or-prefix, not a true glob: a trailing \"*\" wildcard (this app's shared glob convention) is normalized to the literal prefix qwen compares against, but a \"*\" anywhere else in the pattern (e.g. suffix-matching \"* --force\") has no qwen-side equivalent and won't match.",
    },
    costTracking: {
      support: 'partial',
      note: "Estimated, not authoritative: qwen's stream-json result carries usage but no total_cost_usd, so tokens are priced via the same DB-backed pricing table codex_cli uses (cached tokens, when reported, at the cached rate). If the model isn't set, the model qwen actually used (read from the assistant message) is priced instead. Runs on an unpriced model are flagged \"cost unknown\" instead of $0 — add a row for your Qwen model under Settings → Pricing.",
    },
    costWatchdog: {
      support: 'partial',
      note: 'Same mid-run kill switch mechanism as claude (projects cost from incremental token usage). Only effective when the configured model is in the pricing table — otherwise the watchdog is a silent no-op and only the pre-dispatch budget guard applies.',
    },
    imageAttachments: {
      support: 'partial',
      note: "The qwen CLI has no --image flag, but its headless -p prompt supports @path file references the same way the interactive CLI does (verified against the 0.25.2 bundle: handleAtCommand runs unconditionally on the initial prompt in headless mode). Each attachment's worktree copy under .task_attachments/ is referenced with an @ token, and qwen embeds it as real image content — but only if the configured model's modalities report image support; otherwise qwen itself skips the file with a warning and the run continues without it.",
    },
    maxTurns: {
      support: 'full',
      note: 'Enforced via --max-session-turns. Hitting the cap escalates the run to waiting_human instead of retrying (detected from the CLI\'s stderr message, since qwen exits non-zero rather than emitting a structured result).',
    },
    sessionResume: { support: 'full', note: 'session_id + --resume.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
    effort: { support: 'none', note: 'No reasoning-effort flag on the qwen CLI — the field is ignored for this provider.' },
    permissionMode: { support: 'none', note: 'qwen_code always runs in --approval-mode yolo (full auto-approve) — always full bypass, not configurable per agent config.' },
  },
  codex_cli: {
    taskEditorTools: { support: 'full', note: 'All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled).' },
    labelTransitions: { support: 'full' },
    mcpServers: { support: 'none' },
    commandAllowlist: {
      support: 'none',
      note: 'Not enforced for the codex_cli provider — Codex has its own native sandbox/approval-mode system instead (see docs/providers/codex_cli.md).',
    },
    commandDenylist: {
      support: 'none',
      note: 'Not enforced for the codex_cli provider — Codex has its own native sandbox/approval-mode system instead (see docs/providers/codex_cli.md).',
    },
    costTracking: {
      support: 'partial',
      note: 'Estimated, not authoritative like claude/opencode: tokens from turn.completed are priced via the pricing table (DB-backed, with a hardcoded fallback), with cached input tokens at the cached rate. With no model set, the model codex actually used is read from its session file. The pre-dispatch max_cost_usd guard works. Runs on an unpriced model are flagged "cost unknown" instead of $0.',
    },
    costWatchdog: {
      support: 'none',
      note: 'Not implemented — usage is only known at end-of-run for this provider, so mid-run cost can\'t be projected. Only the pre-dispatch budget guard applies.',
    },
    imageAttachments: {
      support: 'full',
      note: 'Passed to codex exec as --image (from the worktree copies under .task_attachments/), so the model sees them visually. Also listed in the prompt as files.',
    },
    maxTurns: {
      support: 'full',
      note: 'Enforced by the runner (codex exec has no turn-cap flag): each completed tool step (command, file edit, MCP call, web search) counts as a turn, and passing the cap stops the run and escalates it to waiting_human instead of retrying.',
    },
    sessionResume: { support: 'full', note: 'thread_id + codex exec resume.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
    effort: {
      support: 'partial',
      note: 'Mapped to the model_reasoning_effort config override (minimal/low/medium/high). Codex has no xhigh/max tier, so those clamp down to high.',
    },
    permissionMode: { support: 'none', note: 'codex_cli always runs with --dangerously-bypass-approvals-and-sandbox — always full bypass, not configurable per agent config.' },
  },
  opencode: {
    taskEditorTools: {
      support: 'full',
      note: 'All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled), delivered through OPENCODE_CONFIG_CONTENT\'s inline "mcp" config since opencode has no --mcp-config flag. The text OUTCOME: success/failure marker is kept as a fallback in case the MCP tool call does not land; opencode\'s own tool-naming has not been verified live, which is exactly why that fallback still matters.',
    },
    labelTransitions: { support: 'full' },
    mcpServers: {
      support: 'none',
      note: 'The task-editor sidecar is now injected (see taskEditorTools), but user-level MCP servers/plugins are still not wired through for this provider.',
    },
    commandAllowlist: {
      support: 'partial',
      note: 'Mapped to opencode\'s "permission.bash" config: listed patterns get "allow" after a catch-all "*": "deny" default. Rules are emitted in opencode\'s last-match-wins order (https://opencode.ai/docs/permissions/): "*" first, allow entries next, deny entries last.',
    },
    commandDenylist: {
      support: 'partial',
      note: 'Mapped to opencode\'s "permission.bash" config as "deny" entries emitted last, so under opencode\'s last-match-wins evaluation they always win over an overlapping allowlist entry. opencode run auto-rejects "ask" permissions headless since --auto is deliberately never passed, so a deny entry genuinely blocks the pattern.',
    },
    costTracking: {
      support: 'full',
      note: 'Authoritative cost and token counts, summed across every step_finish event in the run (cost + tokens.input/output, plus cache read/write into input tokens) — not estimated via a pricing table.',
    },
    costWatchdog: {
      support: 'none',
      note: 'Usage is now recorded at end-of-run (see costTracking), but no mid-run kill switch is wired up for this provider yet — each step_finish only carries that step\'s own cost/token delta, summed into a total only once the run ends, not a continuously-updated running total a watchdog could project from mid-run. Only the pre-dispatch budget guard applies.',
    },
    imageAttachments: {
      support: 'full',
      note: 'Passed to opencode run as -f/--file (from the worktree copies under .task_attachments/), so the model receives them as message attachments. Also listed in the prompt as files.',
    },
    maxTurns: {
      support: 'full',
      note: 'Enforced via agent.<name>.steps in OPENCODE_CONFIG_CONTENT, with the runner also counting step_finish events as a backstop in case the installed opencode version ignores the config key. Hitting the cap escalates the run to waiting_human instead of retrying.',
    },
    sessionResume: { support: 'full', note: 'sessionID + --session.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
    effort: {
      support: 'partial',
      note: 'Passed as opencode run --variant. The value is forwarded to the model provider as-is (low/medium/high/xhigh/max); a model that doesn\'t define that variant may ignore or reject it.',
    },
    permissionMode: { support: 'none', note: 'No permission-mode flag on the opencode CLI — the field is ignored for this provider.' },
  },
}

/** Looks up a single capability entry for a provider, defaulting to unknown/none for unrecognized providers. */
export function getCapability(provider: string, capability: Capability): CapabilityEntry {
  return PROVIDER_CAPABILITIES[provider]?.[capability] ?? { support: 'none' }
}
