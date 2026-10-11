import { describe, expect, it } from 'vitest'
import { escalationInfo } from './escalation'

describe('escalationInfo', () => {
  it('replies to questions, retries failures, sends config problems to settings', () => {
    expect(escalationInfo('agent_question').action).toBe('reply')
    expect(escalationInfo('transient_exhausted').action).toBe('retry')
    const auth = escalationInfo('auth_required')
    expect(auth.action).toBe('fix')
    expect(auth.fix?.to).toBe('/health')
    expect(escalationInfo('cost_budget').fix?.to).toBe('/agents')
  })

  it('falls back to a reply for missing or unknown codes', () => {
    for (const r of [null, undefined, '', 'something_new']) {
      const info = escalationInfo(r)
      expect(info.action).toBe('reply')
      expect(info.label).toBe('Agent is waiting for your input')
    }
  })
})
