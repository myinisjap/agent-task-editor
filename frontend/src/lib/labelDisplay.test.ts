import { describe, it, expect } from 'vitest'
import { humanizeLabelName, labelDisplayName, labelDisplayNameByName } from './labelDisplay'
import type { WorkflowLabel } from '../api/client'

const lbl = (name: string, display_name?: string) => ({ name, display_name }) as WorkflowLabel

describe('labelDisplayName', () => {
  it('uses display_name when set (trimmed)', () => {
    expect(labelDisplayName({ name: 'review-plan', display_name: '  Plan review ' })).toBe('Plan review')
  })
  it('falls back when empty or whitespace', () => {
    expect(labelDisplayName({ name: 'not_ready', display_name: '' })).toBe('Not ready')
    expect(labelDisplayName({ name: 'not_ready', display_name: '   ' })).toBe('Not ready')
    expect(labelDisplayName({ name: 'agent-review' })).toBe('Agent review')
  })
})

describe('humanizeLabelName', () => {
  it('handles the generic rule', () => {
    expect(humanizeLabelName('not_ready')).toBe('Not ready')
    expect(humanizeLabelName('agent-review')).toBe('Agent review')
    expect(humanizeLabelName('review-plan')).toBe('Review plan')
    expect(humanizeLabelName('REVIEW-PLAN')).toBe('Review plan')
    expect(humanizeLabelName('a__b--c  d')).toBe('A b c d')
    expect(humanizeLabelName('')).toBe('')
  })
})

describe('labelDisplayNameByName', () => {
  it('finds the label', () => {
    expect(labelDisplayNameByName('review-plan', [lbl('review-plan', 'Plan review')])).toBe('Plan review')
  })
  it('falls back to humanized when not found', () => {
    expect(labelDisplayNameByName('gone_label', [lbl('x')])).toBe('Gone label')
    expect(labelDisplayNameByName('gone_label', null)).toBe('Gone label')
  })
})
