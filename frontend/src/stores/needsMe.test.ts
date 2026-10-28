import { describe, it, expect, vi, beforeEach } from 'vitest'

const m = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../api/client', () => ({ api: { needsMe: { get: m.get } } }))

import { useNeedsMeStore } from './needsMe'

const base = { repo_id: 'r', workflow_id: 'w', label: 'review', since: '2026-01-01T00:00:00Z' }
const human = { ...base, task_id: 'h', task_title: 'H', reason: 'human_gate' }
const pending = { ...base, task_id: 'p', task_title: 'P', reason: 'auto_approving', due_at: '2026-01-01T00:05:00Z', decision_id: 'd1' }
const approval = { task_id: 'a', task_title: 'A', workflow_id: 'w', label: 'merge', gate_label: 'review', approved_at: '2026-01-01T00:00:00Z', note: '', decision_id: 'd', undoable: true }

beforeEach(async () => {
  m.get.mockResolvedValue({ items: [human, pending], approved_by_policy: [approval] })
  await useNeedsMeStore.getState().refresh()
})

describe('needsMe store', () => {
  it('splits pending and approved from the counted items', () => {
    const s = useNeedsMeStore.getState()
    expect(s.items.map((i) => i.task_id)).toEqual(['h'])
    expect(s.byTask.p).toBeUndefined()
    expect(s.autoApproving.map((i) => i.task_id)).toEqual(['p'])
    expect(s.autoApprovingByTask.p.decision_id).toBe('d1')
    expect(s.approvedByPolicy).toHaveLength(1)
  })

  it('markHeld moves a pending row into items and can revert', () => {
    const revert = useNeedsMeStore.getState().markHeld('p')
    let s = useNeedsMeStore.getState()
    expect(s.autoApproving).toHaveLength(0)
    expect(s.byTask.p.reason).toBe('human_gate')
    expect(s.items.map((i) => i.task_id)).toEqual(['h', 'p'])
    revert()
    s = useNeedsMeStore.getState()
    expect(s.autoApproving).toHaveLength(1)
    expect(s.byTask.p).toBeUndefined()
  })

  it('markUndone drops the approval and can revert', () => {
    const revert = useNeedsMeStore.getState().markUndone('a')
    expect(useNeedsMeStore.getState().approvedByPolicy).toHaveLength(0)
    revert()
    expect(useNeedsMeStore.getState().approvedByPolicy).toHaveLength(1)
  })
})
