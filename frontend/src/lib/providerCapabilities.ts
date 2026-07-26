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
// UI-selectable dropdown (which also includes `openai`, an alias with no
// dedicated capability row — it's treated as the OpenAI-compatible `llm`
// path by the backend).
export const KNOWN_PROVIDERS = ['claude', 'qwen_code', 'gemini_cli', 'codex_cli', 'anthropic', 'llm', 'opencode'] as const

export const PROVIDER_CAPABILITIES: Record<string, ProviderCapabilities> = {
  claude: {
    taskEditorTools: { support: 'full', note: 'All 5 task-editor tools via the MCP sidecar.' },
    labelTransitions: { support: 'full' },
    mcpServers: { support: 'full', note: 'Supports Claude plugins and user-level MCP servers.' },
    commandAllowlist: {
      support: 'partial',
      note: 'Not an effective restriction for the claude provider: the CLI only auto-approves matches, it does not block non-matching commands. Use the denylist instead.',
    },
    commandDenylist: { support: 'full' },
    costTracking: { support: 'full', note: 'Authoritative cost and token counts.' },
    imageAttachments: { support: 'full', note: 'Supported via --image.' },
    maxTurns: { support: 'full' },
    sessionResume: { support: 'full', note: 'session_id + --resume.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
  },
  qwen_code: {
    taskEditorTools: { support: 'full', note: 'All 5 task-editor tools via the MCP sidecar.' },
    labelTransitions: { support: 'full' },
    mcpServers: { support: 'none' },
    commandAllowlist: { support: 'full' },
    commandDenylist: { support: 'none', note: 'Not enforced for the qwen_code provider (no confirmed CLI denylist flag).' },
    costTracking: { support: 'full', note: 'Authoritative cost and token counts.' },
    imageAttachments: { support: 'none', note: 'CLI gap.' },
    maxTurns: { support: 'full' },
    sessionResume: { support: 'partial', note: 'Session recorded, not resumed (no verified CLI flag).' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
  },
  gemini_cli: {
    taskEditorTools: { support: 'full', note: 'All 5 task-editor tools via the MCP sidecar.' },
    labelTransitions: { support: 'full' },
    mcpServers: { support: 'none' },
    commandAllowlist: { support: 'none', note: 'Not enforced for the gemini_cli provider (no confirmed CLI allowlist flag).' },
    commandDenylist: { support: 'none', note: 'Not enforced for the gemini_cli provider (no confirmed CLI denylist flag).' },
    costTracking: { support: 'partial', note: 'Tokens only, no cost — a cost budget cap will not reliably fire.' },
    imageAttachments: { support: 'none', note: 'See docs/providers/gemini_cli.md.' },
    maxTurns: { support: 'full' },
    sessionResume: { support: 'partial', note: 'Thread id recorded, not resumed.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
  },
  codex_cli: {
    taskEditorTools: { support: 'full', note: 'All 5 task-editor tools via the MCP sidecar.' },
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
    imageAttachments: { support: 'none', note: 'See docs/providers/codex_cli.md.' },
    maxTurns: { support: 'full' },
    sessionResume: { support: 'partial', note: 'Thread id recorded, not resumed.' },
    subtasks: { support: 'full', note: 'create_subtask MCP tool available.' },
  },
  anthropic: {
    taskEditorTools: { support: 'partial', note: '4 of 5 native task-editor tools (no resolve_comment/create_subtask).' },
    labelTransitions: { support: 'full', note: 'signal_complete implemented natively.' },
    mcpServers: { support: 'none' },
    commandAllowlist: { support: 'full', note: 'Enforced in Go.' },
    commandDenylist: { support: 'full', note: 'Enforced in Go.' },
    costTracking: { support: 'partial', note: 'Estimated from a pricing table, not authoritative.' },
    imageAttachments: { support: 'none', note: 'Not yet implemented.' },
    maxTurns: { support: 'full', note: 'Enforced via the tool-use loop.' },
    sessionResume: { support: 'none', note: 'Achievable (persist messages) but not yet implemented.' },
    subtasks: { support: 'none', note: 'No create_subtask tool — not available on this provider.' },
  },
  llm: {
    taskEditorTools: { support: 'partial', note: '4 of 5 native task-editor tools (no resolve_comment/create_subtask).' },
    labelTransitions: { support: 'full', note: 'signal_complete implemented natively.' },
    mcpServers: { support: 'none' },
    commandAllowlist: { support: 'full', note: 'Enforced in Go.' },
    commandDenylist: { support: 'full', note: 'Enforced in Go.' },
    costTracking: { support: 'partial', note: 'Estimated from a pricing table, not authoritative.' },
    imageAttachments: { support: 'none', note: 'Not yet implemented (backend-dependent).' },
    maxTurns: { support: 'full', note: 'Enforced via the tool-use loop.' },
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
      support: 'none',
      note: 'Cost is not exposed by the CLI — a cost budget cap will not fire for this provider.',
    },
    imageAttachments: { support: 'none' },
    maxTurns: { support: 'none', note: 'Not enforced.' },
    sessionResume: { support: 'none', note: 'Unverified.' },
    subtasks: { support: 'none', note: 'No create_subtask tool — not available on this provider.' },
  },
}

/** Looks up a single capability entry for a provider, defaulting to unknown/none for unrecognized providers. */
export function getCapability(provider: string, capability: Capability): CapabilityEntry {
  return PROVIDER_CAPABILITIES[provider]?.[capability] ?? { support: 'none' }
}
