import { describe, it, expect } from 'vitest'
import { canHumanMove } from './humanMoves'
import type { WorkflowTransition } from '../api/client'

function tr(from: string, to: string, trigger: WorkflowTransition['trigger_type']): WorkflowTransition {
  return { id: `${from}-${to}`, workflow_id: 'wf', from_label: from, to_label: to, trigger_type: trigger }
}

describe('canHumanMove', () => {
  const transitions = [tr('todo', 'doing', 'human'), tr('doing', 'review', 'agent'), tr('review', 'done', 'both')]

  it('allows any defined edge regardless of trigger_type (matches backend human moves)', () => {
    expect(canHumanMove(transitions, 'todo', 'doing')).toBe(true)
    expect(canHumanMove(transitions, 'doing', 'review')).toBe(true)
    expect(canHumanMove(transitions, 'review', 'done')).toBe(true)
  })

  it('rejects undefined edges and reversed edges', () => {
    expect(canHumanMove(transitions, 'todo', 'done')).toBe(false)
    expect(canHumanMove(transitions, 'doing', 'todo')).toBe(false)
  })

  it('rejects moving to the same label', () => {
    expect(canHumanMove(transitions, 'todo', 'todo')).toBe(false)
    expect(canHumanMove([], 'todo', 'todo')).toBe(false)
  })

  it('does not rule anything out when no transitions are loaded', () => {
    expect(canHumanMove([], 'todo', 'done')).toBe(true)
  })
})
