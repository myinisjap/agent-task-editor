import { describe, it, expect } from 'vitest'
import type { NeedsMeItem, Task, Workflow } from '../api/client'
import { buildInbox, failedEffectTasks, inboxCount, itemActions, sourceEditTasks } from './inbox'
import { formatRelativeAge } from './format'

const item = (over: Partial<NeedsMeItem>): NeedsMeItem => ({
  task_id: 't1', task_title: 'T', repo_id: 'r', workflow_id: 'w', label: 'review',
  reason: 'human_gate', since: '2026-01-01T00:00:00Z', ...over,
})

const wf = (transitions: { from_label: string; path: string }[]) =>
  ({ id: 'w', labels: [], transitions: transitions.map((t) => ({ ...t, trigger_type: 'human' })) }) as unknown as Workflow

const task = (over: Record<string, unknown>) => ({ id: 'a', title: 'A', ...over }) as unknown as Task

describe('failedEffectTasks', () => {
  it('keeps only failed rows, skips archived, sorts oldest first', () => {
    const out = failedEffectTasks([
      task({ id: 'new', side_effects: [{ kind: 'create_pr', status: 'failed', attempts: 5, next_at: '2026-01-02T00:00:00Z' }] }),
      task({ id: 'retrying', side_effects: [{ kind: 'terminal', status: 'pending', attempts: 1, next_at: '2026-01-01T00:00:00Z' }] }),
      task({ id: 'arch', archived: true, side_effects: [{ kind: 'terminal', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z' }] }),
      task({ id: 'old', side_effects: [{ kind: 'terminal', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z' }] }),
      task({ id: 'none' }),
    ])
    expect(out.map((o) => o.task.id)).toEqual(['old', 'new'])
  })
})

describe('sourceEditTasks', () => {
  it('keeps tasks with a held edit, skips archived, oldest first', () => {
    const pend = (created_at: string) => ({ title: 't', description: 'd', type: 'task', created_at })
    const out = sourceEditTasks([
      task({ id: 'new', source_pending_update: pend('2026-01-02T00:00:00Z') }),
      task({ id: 'arch', archived: true, source_pending_update: pend('2026-01-01T00:00:00Z') }),
      task({ id: 'old', source_pending_update: pend('2026-01-01T00:00:00Z') }),
      task({ id: 'none', source_pending_update: null }),
    ])
    expect(out.map((o) => o.task.id)).toEqual(['old', 'new'])
    const g = buildInbox([], [], [], out)
    expect(g.source_edit).toHaveLength(2)
    expect(inboxCount(g)).toBe(2)
  })
})

describe('buildInbox / inboxCount', () => {
  it('groups by kind and totals', () => {
    const g = buildInbox(
      [item({ task_id: 'a', reason: 'waiting_human' }), item({ task_id: 'b' }), item({ task_id: 'c', reason: 'pr_conflict' })],
      failedEffectTasks([task({ side_effects: [{ kind: 'terminal', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z' }] })]),
      [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }],
    )
    expect(g.reply.map((i) => i.task_id)).toEqual(['a'])
    expect(g.approval.map((i) => i.task_id)).toEqual(['b'])
    expect(g.pr_conflict).toHaveLength(1)
    expect(inboxCount(g)).toBe(5)
  })
  it('is empty when nothing waits', () => {
    expect(inboxCount(buildInbox([], [], []))).toBe(0)
  })
})

describe('itemActions', () => {
  it('human gate with a success transition offers approve', () => {
    expect(itemActions(item({}), wf([{ from_label: 'review', path: 'success' }])).approve).toBe(true)
  })
  it('human gate without success transition offers nothing one-click', () => {
    const a = itemActions(item({}), wf([{ from_label: 'review', path: 'failure' }]))
    expect(a).toMatchObject({ approve: false, retry: false, reply: false })
  })
  it('pr conflict is link only', () => {
    expect(itemActions(item({ reason: 'pr_conflict' }), undefined)).toMatchObject({ approve: false, retry: false, reply: false })
  })
  it('waiting run: question replies, retry-type escalation retries, fix-type links', () => {
    expect(itemActions(item({ reason: 'waiting_human', escalation_reason: 'agent_question' }), undefined).reply).toBe(true)
    expect(itemActions(item({ reason: 'waiting_human', escalation_reason: 'no_outcome' }), undefined).retry).toBe(true)
    const fix = itemActions(item({ reason: 'waiting_human', escalation_reason: 'auth_required' }), undefined)
    expect(fix.retry).toBe(true)
    expect(fix.fix?.to).toBe('/settings/health')
  })
})

describe('formatRelativeAge', () => {
  const now = new Date('2026-01-10T12:00:00Z')
  it('formats ages', () => {
    expect(formatRelativeAge('2026-01-10T11:59:40Z', now)).toBe('just now')
    expect(formatRelativeAge('2026-01-10T11:55:00Z', now)).toBe('5m ago')
    expect(formatRelativeAge('2026-01-10T10:00:00Z', now)).toBe('2h ago')
    expect(formatRelativeAge('2026-01-07T12:00:00Z', now)).toBe('3d ago')
    expect(formatRelativeAge('garbage', now)).toBe('just now')
  })
})
