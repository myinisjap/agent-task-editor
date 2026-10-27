import { describe, it, expect } from 'vitest'
import { riskFlagLabel, riskFlagClass } from './riskFlags'

describe('riskFlags', () => {
  it('labels known codes', () => {
    expect(riskFlagLabel('tests_removed')).toBe('Tests removed')
    expect(riskFlagLabel('outside_plan_scope')).toBe('Outside plan')
  })
  it('falls back to the raw code', () => {
    expect(riskFlagLabel('brand_new')).toBe('brand_new')
  })
  it('uses red or amber classes', () => {
    expect(riskFlagClass('migration')).toContain('red')
    expect(riskFlagClass('ci_config')).toContain('amber')
    expect(riskFlagClass('unknown')).toContain('amber')
  })
})
