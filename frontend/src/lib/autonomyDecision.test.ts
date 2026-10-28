import { describe, expect, it } from 'vitest'
import type { AutonomyDecision } from '../api/client'
import { buildBannerModel } from './autonomyDecision'

const d = (mode: AutonomyDecision['mode'], decision: AutonomyDecision['decision'], reasons: [string, string][]): AutonomyDecision => ({
  id: '1', task_id: 't', workflow_id: 'w', label: 'review', mode, decision,
  reasons: reasons.map(([code, detail]) => ({ code, detail })), created_at: '',
})
const o = { alwaysAsk: false, gateName: 'Review' }

describe('buildBannerModel', () => {
  it('approve on_flags with verify pass', () => {
    const m = buildBannerModel(d('on_flags', 'approve', [['no_blocking_flags', 'x']]), { ...o, verification: 'pass' })
    expect(m?.kind).toBe('approve')
    expect(m?.summary.join(' · ')).toBe('Review gate set to Ask on flags · no flags · verify passed')
  })
  it('approve auto', () => {
    const m = buildBannerModel(d('auto', 'approve', [['auto_policy', 'x']]), o)
    expect(m?.summary.join(' · ')).toBe('Review gate set to Auto · no hard stops')
  })
  it('ask with flags and stops', () => {
    const m = buildBannerModel(d('on_flags', 'ask', [
      ['flag_raised', 'migration'], ['verify_failed', 'verification status: timeout'],
      ['cost_exceeded', '$6.10 > $5.00'], ['no_diff', 'x'],
    ]), o)
    expect(m?.kind).toBe('ask')
    expect(m?.flagCodes).toEqual(['migration'])
    expect(m?.stops).toEqual(['Verification timed out', 'Cost over the cap ($6.10 > $5.00)', 'No diff to check for flags'])
  })
  it('null for mode ask and missing decision', () => {
    expect(buildBannerModel(d('ask', 'ask', [['mode_ask', 'x']]), o)).toBeNull()
    expect(buildBannerModel(null, o)).toBeNull()
  })
  it('alwaysAsk flips approve to ask', () => {
    const m = buildBannerModel(d('auto', 'approve', [['auto_policy', 'x']]), { ...o, alwaysAsk: true })
    expect(m?.kind).toBe('ask')
    expect(m?.stops).toEqual(['You marked this task Always ask'])
  })
  it('always ask turned off keeps ask with a note', () => {
    const m = buildBannerModel(d('auto', 'ask', [['always_ask', 'x']]), o)
    expect(m?.kind).toBe('ask')
    expect(m?.summary[1]).toMatch(/turned off/)
  })
})
