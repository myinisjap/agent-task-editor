import { describe, it, expect } from 'vitest'
import { blockReasonLabel, isTransientBlockReason } from './blockReason'
import type { BlockReason } from '../api/client'

function reason(code: BlockReason['code']): BlockReason {
  return { code, message: 'irrelevant' }
}

describe('blockReasonLabel', () => {
  it('returns a short label for every known code', () => {
    const codes: BlockReason['code'][] = [
      'paused',
      'agent_ignore',
      'dependency',
      'retry_backoff',
      'no_config',
      'repo_concurrency',
      'rate_limited',
      'cost_budget',
      'wip_limit',
    ]
    for (const code of codes) {
      expect(blockReasonLabel(reason(code))).not.toBe('')
    }
  })
})

describe('isTransientBlockReason', () => {
  it('is true for rate_limited and retry_backoff', () => {
    expect(isTransientBlockReason(reason('rate_limited'))).toBe(true)
    expect(isTransientBlockReason(reason('retry_backoff'))).toBe(true)
  })

  it('is false for every other code', () => {
    expect(isTransientBlockReason(reason('paused'))).toBe(false)
    expect(isTransientBlockReason(reason('cost_budget'))).toBe(false)
    expect(isTransientBlockReason(reason('wip_limit'))).toBe(false)
    expect(isTransientBlockReason(reason('dependency'))).toBe(false)
    expect(isTransientBlockReason(reason('no_config'))).toBe(false)
    expect(isTransientBlockReason(reason('repo_concurrency'))).toBe(false)
    expect(isTransientBlockReason(reason('agent_ignore'))).toBe(false)
  })
})
