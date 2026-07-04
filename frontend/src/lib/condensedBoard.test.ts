import { describe, it, expect } from 'vitest'
import { computeCondensedGroups } from './condensedBoard'
import type { WorkflowLabel, WorkflowTransition } from '../api/client'

function label(name: string, sortOrder: number, opts: { agentIgnore?: boolean; isTerminal?: boolean } = {}): WorkflowLabel {
  return {
    id: name,
    workflow_id: 'wf',
    name,
    color: '#000000',
    sort_order: sortOrder,
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

describe('computeCondensedGroups', () => {
  it('collapses consecutive pure-agent labels into one agent-group', () => {
    const labels = [label('a', 0), label('b', 1), label('done', 2, { isTerminal: true })]
    const transitions = [transition('a', 'b'), transition('b', 'done')]

    const groups = computeCondensedGroups(labels, transitions)
    expect(groups).toEqual([
      { kind: 'agent-group', labels: [labels[0], labels[1]] },
      { kind: 'single', label: labels[2] },
    ])
  })

  it('sorts labels by sort_order before grouping', () => {
    const done = label('done', 2, { isTerminal: true })
    const a = label('a', 0)
    const b = label('b', 1)
    // Deliberately out of order.
    const groups = computeCondensedGroups([done, b, a], [transition('a', 'b'), transition('b', 'done')])
    expect(groups[0]).toEqual({ kind: 'agent-group', labels: [a, b] })
    expect(groups[1]).toEqual({ kind: 'single', label: done })
  })

  it('gives human-touched labels their own single column and splits agent groups around them', () => {
    const labels = [label('a', 0), label('review', 1), label('b', 2), label('done', 3, { isTerminal: true })]
    const transitions = [
      transition('a', 'review'),
      transition('review', 'b', 'human'), // review is human-touched
      transition('b', 'done'),
    ]
    const groups = computeCondensedGroups(labels, transitions)
    expect(groups.map((g) => g.kind)).toEqual(['agent-group', 'single', 'agent-group', 'single'])
    expect(groups[1]).toEqual({ kind: 'single', label: labels[1] })
  })

  it('gives agent_ignore and sink (no outgoing) labels their own column', () => {
    const labels = [label('inbox', 0, { agentIgnore: true }), label('a', 1), label('sink', 2)]
    // "sink" has no outgoing transition.
    const transitions = [transition('a', 'sink')]
    const groups = computeCondensedGroups(labels, transitions)
    expect(groups[0]).toEqual({ kind: 'single', label: labels[0] }) // agent_ignore
    expect(groups[1]).toEqual({ kind: 'agent-group', labels: [labels[1]] }) // pure agent
    expect(groups[2]).toEqual({ kind: 'single', label: labels[2] }) // sink
  })

  it('treats a lone pure-agent label as a single-label agent-group', () => {
    const labels = [label('a', 0), label('done', 1, { isTerminal: true })]
    const groups = computeCondensedGroups(labels, [transition('a', 'done')])
    expect(groups[0]).toEqual({ kind: 'agent-group', labels: [labels[0]] })
  })

  it("treats 'both'-trigger labels as human-touched", () => {
    const labels = [label('a', 0), label('done', 1, { isTerminal: true })]
    const groups = computeCondensedGroups(labels, [transition('a', 'done', 'both')])
    expect(groups[0]).toEqual({ kind: 'single', label: labels[0] })
  })
})
