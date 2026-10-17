import { describe, it, expect } from 'vitest'
import { providerListGaps } from './providerListGaps'
import { getCapability } from './providerCapabilities'

const base = {
  max_turns: 0,
  max_cost_usd: 0,
  effort: '' as const,
  permission_mode: '' as const,
  enabled_plugins: '[]',
  enabled_mcp_servers: '[]',
  command_allowlist: '[]',
  command_denylist: '[]',
  subtasks_enabled: false,
  resume_sessions: false,
}

describe('providerListGaps', () => {
  it('reports nothing when no setting is in use', () => {
    expect(providerListGaps(base, ['claude', 'codex_cli', 'opencode', 'qwen_code'])).toEqual([])
  })

  it('flags a setting a fallback provider does not fully support', () => {
    // effort is claude-specific; pick any provider that does not fully support it.
    const other = ['qwen_code', 'codex_cli', 'opencode'].find((p) => getCapability(p, 'effort').support !== 'full')
    expect(other).toBeDefined()
    const gaps = providerListGaps({ ...base, effort: 'high' }, ['claude', other!])
    expect(gaps).toHaveLength(1)
    expect(gaps[0]).toMatchObject({ provider: other, setting: 'Effort' })
  })

  it('skips providers that fully support every setting in use and de-duplicates providers', () => {
    const gaps = providerListGaps({ ...base, effort: 'high' }, ['claude', 'claude', ''])
    expect(gaps).toEqual([])
  })

  it('treats unknown providers as unsupported', () => {
    const gaps = providerListGaps({ ...base, max_turns: 10 }, ['mystery'])
    expect(gaps).toEqual([{ provider: 'mystery', setting: 'Max turns', support: 'none', note: undefined }])
  })

  it('ignores malformed JSON lists', () => {
    expect(providerListGaps({ ...base, command_allowlist: 'not json' }, ['claude'])).toEqual([])
  })
})
