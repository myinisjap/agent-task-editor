import { describe, it, expect } from 'vitest'
import { validateWorkflow } from './validateWorkflow'
import type { ParsedWorkflow, ParsedWorkflowLabel } from './parseWorkflowYaml'

function label(name: string, sortOrder: number, opts: Partial<ParsedWorkflowLabel> = {}): ParsedWorkflowLabel {
  return { name, sortOrder, isTerminal: false, agentIgnore: false, ...opts }
}

function wf(labels: ParsedWorkflowLabel[], transitions: { from: string; to: string }[]): ParsedWorkflow {
  return { name: 'wf', labels, transitions }
}

describe('validateWorkflow', () => {
  it('returns no errors for a valid linear workflow', () => {
    const workflow = wf(
      [label('start', 0), label('work', 1), label('done', 2, { isTerminal: true })],
      [
        { from: 'start', to: 'work' },
        { from: 'work', to: 'done' },
      ],
    )
    expect(validateWorkflow(workflow)).toEqual([])
  })

  it('requires at least one label', () => {
    const errors = validateWorkflow(wf([], []))
    expect(errors).toEqual([{ message: 'Workflow must have at least one label.' }])
  })

  it('flags a label unreachable from the start label', () => {
    const workflow = wf(
      [label('start', 0), label('orphan', 1)],
      [],
    )
    const errors = validateWorkflow(workflow)
    expect(errors).toHaveLength(1)
    expect(errors[0]).toMatchObject({ label: 'orphan' })
    expect(errors[0].message).toContain('not reachable from the start label "start"')
  })

  it('treats the lowest sort_order label as the start regardless of array order', () => {
    // "start" has the lowest sortOrder but appears last in the array.
    const workflow = wf(
      [label('mid', 1), label('end', 2), label('start', 0)],
      [
        { from: 'start', to: 'mid' },
        { from: 'mid', to: 'end' },
      ],
    )
    expect(validateWorkflow(workflow)).toEqual([])
  })

  it('flags transitions referencing unknown labels', () => {
    const workflow = wf(
      [label('start', 0)],
      [{ from: 'start', to: 'ghost' }],
    )
    const messages = validateWorkflow(workflow).map((e) => e.message)
    expect(messages).toContain('Transition references unknown label "ghost".')
  })

  it('flags a terminal label that has outgoing transitions', () => {
    const workflow = wf(
      [label('start', 0), label('done', 1, { isTerminal: true })],
      [
        { from: 'start', to: 'done' },
        { from: 'done', to: 'start' },
      ],
    )
    const err = validateWorkflow(workflow).find((e) => e.label === 'done')!
    expect(err.message).toContain('Terminal label "done" has outgoing transition(s) to "start"')
  })

  it('reports reachability and terminal violations independently', () => {
    const workflow = wf(
      [
        label('start', 0),
        label('done', 1, { isTerminal: true }),
        label('orphan', 2, { isTerminal: true }),
      ],
      [
        { from: 'start', to: 'done' },
        { from: 'orphan', to: 'start' }, // orphan is terminal AND unreachable
      ],
    )
    const errors = validateWorkflow(workflow)
    const orphanErrors = errors.filter((e) => e.label === 'orphan')
    expect(orphanErrors).toHaveLength(2)
    expect(orphanErrors.some((e) => e.message.includes('not reachable'))).toBe(true)
    expect(orphanErrors.some((e) => e.message.includes('has outgoing transition'))).toBe(true)
  })

  it('de-duplicates multiple outgoing targets of a terminal label in the message', () => {
    const workflow = wf(
      [label('start', 0), label('done', 1, { isTerminal: true })],
      [
        { from: 'start', to: 'done' },
        { from: 'done', to: 'start' },
        { from: 'done', to: 'start' },
      ],
    )
    const err = validateWorkflow(workflow).find((e) => e.label === 'done')!
    // "start" should appear only once even though there are two transitions.
    expect(err.message.match(/"start"/g)).toHaveLength(1)
  })
})
