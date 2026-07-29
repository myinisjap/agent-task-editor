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

export type Support = 'full' | 'partial' | 'none'

export interface CapabilityEntry {
  support: Support
  /** Short human-readable explanation, surfaced verbatim in the UI. */
  note?: string
}

export type ProviderCapabilities = Partial<Record<Capability, CapabilityEntry>>

// Ordered list of known providers, matching docs/agents.md's Capability
// Matrix column order. `agentTemplates.ts`'s PROVIDERS list is the
// UI-selectable dropdown; it no longer includes `anthropic`, `llm`, or the
// `openai` alias for the same OpenAI-compatible path — all three are
// deprecated (see DEPRECATED_PROVIDERS below) and no longer selectable for
// new/updated configs. They're kept here so existing configs on those
// providers still get their capability rows and warnings rendered.
export const KNOWN_PROVIDERS = ['claude', 'qwen_code', 'codex_cli', 'anthropic', 'llm', 'opencode'] as const

// Providers disabled for new/updated provider configs (rejected by the
// backend) and hidden from agentTemplates.ts's PROVIDERS dropdown, but still
// present in PROVIDER_CAPABILITIES above so existing configs on these
// providers keep rendering accurate capability warnings and model lists.
// "openai" was a dead dropdown alias for the same deprecated `llm` path and
// never had its own capability row. See docs/providers/anthropic.md and
// docs/providers/llm.md for the deprecation notice.
export const DEPRECATED_PROVIDERS = new Set(['anthropic', 'llm', 'openai'])

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
      note: "Enforced via qwen's --exclude-tools flag (folds into its permissionsDeny policy), which is honored even under yolo mode. Per-pattern Bash(pattern) granularity mirrors --allowed-tools but has not been confirmed live for the deny path; if qwen only accepts bare tool names here, denial may degrade to blanket Bash exclusion.",
    },
    costTracking: {
      support: 'partial',
      note: "Tokens only, no cost — qwen's stream-json result carries usage but no total_cost_usd, so a cost budget cap will not reliably fire.",
    },
    costWatchdog: {
      support: 'partial',
      note: 'Same mid-run kill switch mechanism as claude (projects cost from incremental token usage). Only effective when the configured model is in the pricing table — otherwise the watchdog is a silent no-op and only the pre-dispatch budget guard applies.',
    },
    imageAttachments: { support: 'none', note: 'No image flag on the qwen CLI.' },
    maxTurns: { support: 'full', note: 'Enforced via --max-session-turns. Hitting the cap escalates the run to waiting_human instead of retrying.' },
    sessionResume: { support: 'full', note: 'session_id + --resume.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
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
    costTracking: { support: 'partial', note: 'Tokens only, no cost — a cost budget cap will not reliably fire.' },
    costWatchdog: {
      support: 'none',
      note: 'Not implemented — codex captures tokens but does not price them (blocked on pricing-table support for this provider, see #245), so mid-run cost can\'t be projected. Only the pre-dispatch budget guard applies.',
    },
    imageAttachments: {
      support: 'none',
      note: 'codex exec has an -i/--image flag, but attachments are not wired through to it yet. See docs/providers/codex_cli.md.',
    },
    maxTurns: {
      support: 'none',
      note: 'Not enforced — codex exec has no turn-cap flag, so only the run timeout bounds a run.',
    },
    sessionResume: { support: 'full', note: 'thread_id + codex exec resume.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
  },
  anthropic: {
    taskEditorTools: { support: 'partial', note: '5 of 7 task-editor tools implemented natively (no resolve_comment/create_subtask).' },
    labelTransitions: { support: 'full', note: 'signal_complete implemented natively.' },
    mcpServers: { support: 'none' },
    commandAllowlist: { support: 'full', note: 'Enforced in Go.' },
    commandDenylist: { support: 'full', note: 'Enforced in Go.' },
    costTracking: { support: 'partial', note: 'Estimated from a pricing table, not authoritative.' },
    costWatchdog: { support: 'none', note: 'No mid-run kill switch implemented — only the pre-dispatch budget guard applies.' },
    imageAttachments: { support: 'none', note: 'Not yet implemented.' },
    maxTurns: { support: 'full', note: 'Enforced via the tool-use loop. Hitting the cap escalates the run to waiting_human instead of retrying.' },
    sessionResume: { support: 'none', note: 'Achievable (persist messages) but not yet implemented.' },
    subtasks: { support: 'none', note: 'No create_subtask tool — not available on this provider.' },
  },
  llm: {
    taskEditorTools: { support: 'partial', note: '5 of 7 task-editor tools implemented natively (no resolve_comment/create_subtask).' },
    labelTransitions: { support: 'full', note: 'signal_complete implemented natively.' },
    mcpServers: { support: 'none' },
    commandAllowlist: { support: 'full', note: 'Enforced in Go.' },
    commandDenylist: { support: 'full', note: 'Enforced in Go.' },
    costTracking: { support: 'partial', note: 'Estimated from a pricing table, not authoritative.' },
    costWatchdog: { support: 'none', note: 'No mid-run kill switch implemented — only the pre-dispatch budget guard applies.' },
    imageAttachments: { support: 'none', note: 'Not yet implemented (backend-dependent).' },
    maxTurns: { support: 'full', note: 'Enforced via the tool-use loop. Hitting the cap escalates the run to waiting_human instead of retrying.' },
    sessionResume: { support: 'none', note: 'Achievable (persist messages) but not yet implemented.' },
    subtasks: { support: 'none', note: 'No create_subtask tool — not available on this provider.' },
  },
  opencode: {
    taskEditorTools: {
      support: 'none',
      note: 'No MCP tools — relies on a text OUTCOME: success/failure marker instead of task-editor tool calls.',
    },
    labelTransitions: {
      support: 'none',
      note: 'Cannot signal workflow transitions via MCP tools; tasks handled by this agent may not move to the next label automatically.',
    },
    mcpServers: { support: 'none', note: 'MCP servers / plugins are not supported by the opencode provider.' },
    commandAllowlist: { support: 'none', note: 'Not enforced for the opencode provider.' },
    commandDenylist: { support: 'none', note: 'Not enforced for the opencode provider.' },
    costTracking: {
      support: 'full',
      note: "Authoritative cost and token counts, read directly from the CLI's step_finish event (cost + tokens.input/output) — not estimated via a pricing table.",
    },
    costWatchdog: {
      support: 'none',
      note: 'Usage is now recorded at end-of-run (see costTracking), but no mid-run kill switch is wired up for this provider yet — step_finish only carries a cumulative-to-date snapshot, not the per-turn incremental usage a watchdog needs to project a running total. Only the pre-dispatch budget guard applies.',
    },
    imageAttachments: { support: 'none', note: 'opencode run has an -f/--file flag, but attachments are not wired through to it.' },
    maxTurns: { support: 'none', note: 'Not enforced — the opencode CLI has no turn-cap flag.' },
    sessionResume: { support: 'full', note: 'sessionID + --session.' },
    subtasks: { support: 'none', note: 'No create_subtask tool — not available on this provider.' },
  },
}

/** Looks up a single capability entry for a provider, defaulting to unknown/none for unrecognized providers. */
export function getCapability(provider: string, capability: Capability): CapabilityEntry {
  return PROVIDER_CAPABILITIES[provider]?.[capability] ?? { support: 'none' }
}
