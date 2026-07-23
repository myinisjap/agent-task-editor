import { describe, it, expect } from 'vitest'
import { isHumanGateLabel, findLabel } from './humanGate'
import type { Workflow, WorkflowLabel, WorkflowTransition } from '../api/client'

function label(name: string, opts: { agentIgnore?: boolean; isTerminal?: boolean } = {}): WorkflowLabel {
  return {
    id: name,
    workflow_id: 'wf',
    name,
    color: '#000000',
    sort_order: 0,
    agent_ignore: opts.agentIgnore ? 1 : 0,
    is_terminal: opts.isTerminal ? 1 : 0,
  }
}

function transition(
  from: string,
  to: string,
  trigger: WorkflowTransition['trigger_type'] = 'agent',
): WorkflowTransition {
  return { id: `${from}->${to}`, workflow_id: 'wf', from_label: from, to_label: to, trigger_type: trigger }
}

function workflow(labels: WorkflowLabel[], transitions: WorkflowTransition[]): Workflow {
  return {
    id: 'wf',
    name: 'wf',
    description: '',
    labels,
    transitions,
    created_at: '',
    updated_at: '',
  }
}

describe('isHumanGateLabel', () => {
  it('returns false when workflow is undefined', () => {
    expect(isHumanGateLabel(undefined, 'review')).toBe(false)
  })

  it('returns false when the label does not exist in the workflow', () => {
    const wf = workflow([label('a')], [])
    expect(isHumanGateLabel(wf, 'missing')).toBe(false)
  })

  it('is a gate when agent_ignore is set, regardless of transitions', () => {
    const wf = workflow(
      [label('review', { agentIgnore: true })],
      [transition('review', 'done', 'agent')],
    )
    expect(isHumanGateLabel(wf, 'review')).toBe(true)
  })

  it('is a gate when every outgoing transition is human-only', () => {
    const wf = workflow(
      [label('review'), label('done', { isTerminal: true })],
      [transition('review', 'done', 'human')],
    )
    expect(isHumanGateLabel(wf, 'review')).toBe(true)
  })

  it('is not a gate when an outgoing transition is agent or both', () => {
    const wfAgent = workflow(
      [label('review'), label('done', { isTerminal: true })],
      [transition('review', 'x', 'human'), transition('review', 'done', 'agent')],
    )
    expect(isHumanGateLabel(wfAgent, 'review')).toBe(false)

    const wfBoth = workflow(
      [label('review'), label('done', { isTerminal: true })],
      [transition('review', 'done', 'both')],
    )
    expect(isHumanGateLabel(wfBoth, 'review')).toBe(false)
  })

  it('is not a gate for a terminal label even with no outgoing transitions', () => {
    const wf = workflow([label('done', { isTerminal: true })], [])
    expect(isHumanGateLabel(wf, 'done')).toBe(false)
  })

  it('is not a gate for a non-terminal label with zero outgoing transitions (dead end)', () => {
    const wf = workflow([label('stuck')], [])
    expect(isHumanGateLabel(wf, 'stuck')).toBe(false)
  })
})

describe('findLabel', () => {
  it('finds a label by name', () => {
    const wf = workflow([label('review')], [])
    expect(findLabel(wf, 'review')?.name).toBe('review')
  })

  it('returns undefined when workflow is undefined or label missing', () => {
    expect(findLabel(undefined, 'review')).toBeUndefined()
    const wf = workflow([label('review')], [])
    expect(findLabel(wf, 'missing')).toBeUndefined()
  })
})
