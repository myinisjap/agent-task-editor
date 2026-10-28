import { describe, expect, it } from 'vitest'
import type { WSEvent } from '../api/ws'
import { formatTickerEvent, pushTicker, type TickerItem } from './homeTicker'

const labels = [
  { name: 'testing', display_name: 'Testing' },
  { name: 'work', display_name: null },
] as never
const ctx = { titleFor: (id: string) => (id === 't1' ? 'Fix flaky login test' : undefined), labels }
const f = (type: string, payload: object) => formatTickerEvent({ type, payload } as unknown as WSEvent, ctx, 1000)

describe('formatTickerEvent', () => {
  it('label moves use display names', () => {
    expect(f('task.label_changed', { task_id: 't1', from: 'testing', to: 'work', note: '' })?.text).toBe('Fix flaky login test moved Testing → Work')
  })
  it('agent_done statuses', () => {
    expect(f('task.agent_done', { task_id: 't1', run_id: 'r', status: 'completed' })?.text).toBe('Fix flaky login test: agent finished')
    expect(f('task.agent_done', { task_id: 't1', run_id: 'r', status: 'failed' })?.text).toContain('agent failed')
    expect(f('task.agent_done', { task_id: 't1', run_id: 'r', status: 'cancelled' })?.text).toContain('run cancelled')
    expect(f('task.agent_done', { task_id: 't1', run_id: 'r', status: 'waiting_human' })?.text).toContain('waiting on you')
  })
  it('verification statuses', () => {
    const t = (status: string) => f('task.verification', { task_id: 't1', run_id: 'r', status })?.text
    expect(t('pass')).toContain('verify passed')
    expect(t('fail')).toContain('verify failed')
    expect(t('timeout')).toContain('verify timed out')
    expect(t('error')).toContain('verify errored')
  })
  it('pr mergeable', () => {
    expect(f('task.pr_mergeable_changed', { task_id: 't1', pr_mergeable: 'conflicting' })?.text).toContain('PR has conflicts')
    expect(f('task.pr_mergeable_changed', { task_id: 't1', pr_mergeable: 'mergeable' })?.text).toContain('PR is mergeable')
    expect(f('task.pr_mergeable_changed', { task_id: 't1', pr_mergeable: '' })).toBeNull()
    expect(f('task.pr_mergeable_changed', { task_id: 't1', pr_mergeable: 'unknown' })).toBeNull()
  })
  it('needs_human, provider, unknown, fallback title', () => {
    const n = f('task.needs_human', { task_id: 't1', run_id: 'r', reason: 'cost_budget', message: '' })
    expect(n?.text).toBe('Fix flaky login test needs you')
    expect(n?.tone).toBe('gate')
    expect(f('provider.pause_changed', { provider: 'claude', paused: true })?.text).toBe('claude paused')
    expect(f('provider.pause_changed', { provider: 'claude', paused: false })?.text).toBe('claude resumed')
    expect(f('task.updated', {})).toBeNull()
    expect(f('task.needs_human', { task_id: 'abcdef123456', run_id: 'r', reason: 'cost_budget', message: '' })?.text).toBe('Task abcdef12 needs you')
  })
})

describe('pushTicker', () => {
  it('caps at 3, newest first', () => {
    let l: TickerItem[] = []
    for (let i = 0; i < 5; i++) l = pushTicker(l, { id: String(i), text: String(i), at: i })
    expect(l.map((x) => x.id)).toEqual(['4', '3', '2'])
  })
})
