import { describe, it, expect } from 'vitest'
import { PROVIDER_CAPABILITIES, KNOWN_PROVIDERS, getCapability } from './providerCapabilities'

describe('providerCapabilities', () => {
  it('has an entry for every known provider', () => {
    for (const p of KNOWN_PROVIDERS) {
      expect(PROVIDER_CAPABILITIES[p]).toBeDefined()
    }
  })

  it('opencode lacks label transitions and MCP support, but has full cost tracking', () => {
    expect(getCapability('opencode', 'labelTransitions').support).toBe('none')
    expect(getCapability('opencode', 'mcpServers').support).toBe('none')
    // opencode's step_finish event reports authoritative cost/tokens
    // directly from the CLI (see parse_opencode.go / #287), so this is
    // 'full', unlike the label/MCP gaps above.
    expect(getCapability('opencode', 'costTracking').support).toBe('full')
  })

  it('claude has full support for MCP-backed capabilities', () => {
    expect(getCapability('claude', 'mcpServers').support).toBe('full')
    expect(getCapability('claude', 'labelTransitions').support).toBe('full')
    expect(getCapability('claude', 'subtasks').support).toBe('full')
  })

  it('falls back to none for an unrecognized provider', () => {
    expect(getCapability('made-up-provider', 'mcpServers')).toEqual({ support: 'none' })
  })

  it('every partial-support entry carries an explanatory note', () => {
    // "partial" is inherently ambiguous (some support, some gap) so it must
    // explain itself; "none" is self-explanatory enough to allow a bare
    // entry (e.g. "no MCP servers at all").
    for (const provider of KNOWN_PROVIDERS) {
      const caps = PROVIDER_CAPABILITIES[provider]
      for (const [capability, entry] of Object.entries(caps)) {
        if (entry && entry.support === 'partial') {
          expect(entry.note, `${provider}.${capability} should explain its partial support`).toBeTruthy()
        }
      }
    }
  })
})
