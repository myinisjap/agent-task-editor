// Which of an agent config's settings each provider in its ordered provider
// list (primary + fallbacks) can't fully honour. A task can land on any of
// them when an earlier provider is rate-limited, so a setting that only the
// primary supports silently stops applying on a fallback. Driven entirely by
// providerCapabilities.ts.
import type { AgentConfig } from '../api/client'
import { getCapability, type Capability, type Support } from './providerCapabilities'

type GapForm = Pick<
  AgentConfig,
  | 'max_turns'
  | 'max_cost_usd'
  | 'effort'
  | 'permission_mode'
  | 'enabled_plugins'
  | 'enabled_mcp_servers'
  | 'command_allowlist'
  | 'command_denylist'
  | 'subtasks_enabled'
  | 'resume_sessions'
>

export interface SettingGap {
  /** Provider string, e.g. "codex_cli". */
  provider: string
  /** Human-readable setting name, e.g. "Effort". */
  setting: string
  support: Exclude<Support, 'full'>
  note?: string
}

function nonEmptyJSONList(raw: string | undefined): boolean {
  try {
    const v = JSON.parse(raw ?? '[]')
    return Array.isArray(v) && v.length > 0
  } catch {
    return false
  }
}

// The settings the form has turned on, each with the capability it needs.
function settingsInUse(form: GapForm): Array<{ setting: string; capability: Capability }> {
  const used: Array<{ setting: string; capability: Capability }> = []
  if ((form.max_turns ?? 0) > 0) used.push({ setting: 'Max turns', capability: 'maxTurns' })
  if ((form.max_cost_usd ?? 0) > 0) used.push({ setting: 'Cost budget (mid-run kill)', capability: 'costWatchdog' })
  if (form.effort) used.push({ setting: 'Effort', capability: 'effort' })
  if (form.permission_mode) used.push({ setting: 'Permission mode', capability: 'permissionMode' })
  if (nonEmptyJSONList(form.enabled_plugins) || nonEmptyJSONList(form.enabled_mcp_servers)) {
    used.push({ setting: 'Plugins / MCP servers', capability: 'mcpServers' })
  }
  if (nonEmptyJSONList(form.command_allowlist)) used.push({ setting: 'Command allowlist', capability: 'commandAllowlist' })
  if (nonEmptyJSONList(form.command_denylist)) used.push({ setting: 'Command denylist', capability: 'commandDenylist' })
  if (form.subtasks_enabled) used.push({ setting: 'Subtasks', capability: 'subtasks' })
  if (form.resume_sessions) used.push({ setting: 'Session resume', capability: 'sessionResume' })
  return used
}

/**
 * Returns every (provider, setting) pair where a provider in the list
 * doesn't fully support a setting the form uses. Providers are de-duplicated
 * and kept in list order; empty provider strings are skipped.
 */
export function providerListGaps(form: GapForm, providers: string[]): SettingGap[] {
  const used = settingsInUse(form)
  const gaps: SettingGap[] = []
  const seen = new Set<string>()
  for (const provider of providers) {
    if (!provider || seen.has(provider)) continue
    seen.add(provider)
    for (const { setting, capability } of used) {
      const cap = getCapability(provider, capability)
      if (cap.support === 'full') continue
      gaps.push({ provider, setting, support: cap.support, note: cap.note })
    }
  }
  return gaps
}
