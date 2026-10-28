import { describe, expect, it } from 'vitest'
import { escalationInfo, ESCALATION_REASONS } from './escalation'

describe('escalationInfo', () => {
  it('replies to questions, retries failures, sends config problems to settings', () => {
    expect(escalationInfo('agent_question').action).toBe('reply')
    expect(escalationInfo('transient_exhausted').action).toBe('retry')
    expect(escalationInfo('verify_error').action).toBe('fix')
    const auth = escalationInfo('auth_required')
    expect(auth.action).toBe('fix')
    expect(auth.fix?.to).toBe('/settings/health')
    expect(escalationInfo('cost_budget').fix?.to).toBe('/pipeline/agents')
  })

  it('falls back to a reply for missing or unknown codes', () => {
    for (const r of [null, undefined, '', 'something_new']) {
      const info = escalationInfo(r)
      expect(info.action).toBe('reply')
      expect(info.label).toBe('Agent is waiting for your input')
    }
  })
})

describe('escalation wording', () => {
  it('every reason has full wording', () => {
    expect(ESCALATION_REASONS.length).toBeGreaterThan(0)
    for (const r of ESCALATION_REASONS) {
      const i = escalationInfo(r)
      expect(i.short).toBeTruthy()
      expect(i.label).toBeTruthy()
      expect(i.explanation).toBeTruthy()
      expect(i.nextStep).toBeTruthy()
      if (i.action === 'fix') expect(i.fix).toBeTruthy()
    }
    const fb = escalationInfo('nope')
    expect(fb.explanation).toBeTruthy()
    expect(fb.nextStep).toBeTruthy()
  })
})
