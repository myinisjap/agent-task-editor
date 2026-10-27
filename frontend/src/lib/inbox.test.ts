import { describe, it, expect } from 'vitest'
import type { NeedsMeItem, Task, Workflow } from '../api/client'
import { buildInbox, entryKey, failedEffectTasks, flattenInbox, inboxCount, itemActions, neighborKey, nextAfterRemoval, resolveSelection, sourceEditTasks } from './inbox'
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
    expect(g.decide).toHaveLength(2)
    expect(inboxCount(g)).toBe(2)
  })
})

const ids = (es: { kind: string; item?: NeedsMeItem }[]) => es.map((e) => e.item?.task_id ?? e.kind)

describe('buildInbox sections / inboxCount', () => {
  it('classifies needs-me items by action', () => {
    const g = buildInbox(
      [
        item({ task_id: 'gate' }),
        item({ task_id: 'q', reason: 'waiting_human', escalation_reason: 'agent_question' }),
        item({ task_id: 'loop', reason: 'waiting_human', escalation_reason: 'rework_loop' }),
        item({ task_id: 'nocode', reason: 'waiting_human' }),
        item({ task_id: 'auth', reason: 'waiting_human', escalation_reason: 'auth_required' }),
        item({ task_id: 'rt', reason: 'waiting_human', escalation_reason: 'runtime_prep' }),
        item({ task_id: 'budget', reason: 'waiting_human', escalation_reason: 'cost_budget' }),
        item({ task_id: 'rep', reason: 'waiting_human', escalation_reason: 'repeated_failure' }),
        item({ task_id: 'pr', reason: 'pr_conflict' }),
      ],
      [],
      [],
    )
    expect(ids(g.decide as never)).toEqual(['gate'])
    expect(ids(g.answer as never)).toEqual(['q', 'loop', 'nocode'])
    expect(ids(g.fix as never)).toEqual(['auth', 'rt', 'budget', 'rep', 'pr'])
  })

  it('puts source edits in decide and failed effects / paused providers in fix', () => {
    const edits = sourceEditTasks([task({ id: 'e', source_pending_update: { title: 't', description: 'd', type: 'task', created_at: '2026-01-01T00:00:00Z' } })])
    const failed = failedEffectTasks([task({ side_effects: [{ kind: 'terminal', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z' }] })])
    const g = buildInbox([], failed, [{ provider: 'claude', reason: 'x', paused_at: '2026-01-02T00:00:00Z' }], edits)
    expect(g.decide.map((e) => e.kind)).toEqual(['source_edit'])
    expect(g.fix.map((e) => e.kind)).toEqual(['side_effect', 'provider'])
  })

  it('sorts oldest first within a section', () => {
    const g = buildInbox(
      [item({ task_id: 'new', since: '2026-01-03T00:00:00Z' }), item({ task_id: 'old', since: '2026-01-01T00:00:00Z' })],
      [],
      [],
    )
    expect(ids(g.decide as never)).toEqual(['old', 'new'])
  })

  it('count equals every input and the sum of the sections (no 4-vs-3 mismatch)', () => {
    const needsMe = [item({ task_id: 'a', reason: 'waiting_human' }), item({ task_id: 'b' }), item({ task_id: 'c', reason: 'pr_conflict' })]
    const failed = failedEffectTasks([task({ side_effects: [{ kind: 'terminal', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z' }] })])
    const paused = [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }]
    const g = buildInbox(needsMe, failed, paused)
    expect(inboxCount(g)).toBe(5)
    expect(g.decide.length + g.answer.length + g.fix.length).toBe(5)
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

describe('itemActions requestChanges', () => {
  it('is offered on a gate only when a human failure transition exists', () => {
    expect(itemActions(item({}), wf([{ from_label: 'review', path: 'success' }])).requestChanges).toBe(false)
    const a = itemActions(item({}), wf([{ from_label: 'review', path: 'success' }, { from_label: 'review', path: 'failure' }]))
    expect(a).toMatchObject({ approve: true, requestChanges: true })
  })
})

describe('selection helpers', () => {
  const sections = () =>
    buildInbox(
      [item({ task_id: 'g1', since: '2026-01-02T00:00:00Z' }), item({ task_id: 'g2', since: '2026-01-03T00:00:00Z' }), item({ task_id: 'f1', reason: 'pr_conflict' })],
      [{ task: task({ id: 'g1' }), effects: [], since: '2026-01-01T00:00:00Z' }],
      [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }],
      [{ task: task({ id: 's1' }), since: '2026-01-01T00:00:00Z' }],
    )

  it('builds stable keys', () => {
    const keys = flattenInbox(sections()).map(entryKey)
    expect(keys).toEqual(['s1:source', 'g1', 'g2', 'f1', 'g1:effects', 'provider:claude'])
  })

  it('resolves exact key, task id, then the first entry', () => {
    const flat = flattenInbox(sections())
    expect(resolveSelection(flat, 'g2')).toBe('g2')
    expect(resolveSelection(flat, 'g1:effects')).toBe('g1:effects')
    expect(resolveSelection(flat, 's1')).toBe('s1:source')
    expect(resolveSelection(flat, 'gone')).toBe('s1:source')
    expect(resolveSelection(flat, null)).toBe('s1:source')
    expect(resolveSelection([], 'g1')).toBeUndefined()
  })

  it('clamps neighbours', () => {
    const flat = flattenInbox(sections())
    expect(neighborKey(flat, 'g1', 1)).toBe('g2')
    expect(neighborKey(flat, 's1:source', -1)).toBe('s1:source')
    expect(neighborKey(flat, 'provider:claude', 1)).toBe('provider:claude')
  })

  it('picks the entry after a removal, else the one before', () => {
    const flat = flattenInbox(sections())
    expect(nextAfterRemoval(flat, 'g1')).toBe('g2')
    expect(nextAfterRemoval(flat, 'provider:claude')).toBe('g1:effects')
    expect(nextAfterRemoval(flat.slice(0, 1), 's1:source')).toBeUndefined()
  })
})
