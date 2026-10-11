import { describe, expect, it } from 'vitest'
import { needsMeReasonLabel } from './needsMe'

describe('needsMeReasonLabel', () => {
  it('maps each reason code', () => {
    expect(needsMeReasonLabel('waiting_human')).toBe('Agent asked a question')
    expect(needsMeReasonLabel('human_gate')).toBe('Waiting for your review')
    expect(needsMeReasonLabel('pr_conflict')).toBe('PR has conflicts')
  })
})
