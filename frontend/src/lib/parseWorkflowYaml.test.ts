import { describe, it, expect } from 'vitest'
import { parseWorkflowYaml } from './parseWorkflowYaml'

const FULL_WORKFLOW = `name: Default
description: |
  a description line that the parser ignores
labels:
  - name: not_ready
    sort_order: 0
    is_terminal: false
    agent_ignore: true
  - name: work
    sort_order: 1
  - name: done
    sort_order: 2
    is_terminal: true
transitions:
  - from: not_ready
    to: work
  - from: work
    to: done
`

describe('parseWorkflowYaml', () => {
  it('parses name, labels, and transitions from a full workflow', () => {
    const wf = parseWorkflowYaml(FULL_WORKFLOW)
    expect(wf.name).toBe('Default')
    expect(wf.labels).toHaveLength(3)
    expect(wf.transitions).toEqual([
      { from: 'not_ready', to: 'work' },
      { from: 'work', to: 'done' },
    ])
  })

  it('coerces scalar types (booleans, integers, defaults)', () => {
    const wf = parseWorkflowYaml(FULL_WORKFLOW)
    const notReady = wf.labels[0]
    expect(notReady).toEqual({ name: 'not_ready', sortOrder: 0, isTerminal: false, agentIgnore: true })

    // "work" omits is_terminal/agent_ignore → default false; sort_order parses to a number.
    expect(wf.labels[1]).toEqual({ name: 'work', sortOrder: 1, isTerminal: false, agentIgnore: false })
    expect(wf.labels[2].isTerminal).toBe(true)
  })

  it('handles quoted strings and normalises CRLF line endings', () => {
    const yaml = 'name: "My Flow"\r\nlabels:\r\n  - name: \'start\'\r\n    sort_order: 0\r\n'
    const wf = parseWorkflowYaml(yaml)
    expect(wf.name).toBe('My Flow')
    expect(wf.labels[0].name).toBe('start')
  })

  it('strips trailing comments outside of quotes', () => {
    const yaml = 'name: Flow # this is the name\nlabels:\n  - name: a # first label\n    sort_order: 0\n'
    const wf = parseWorkflowYaml(yaml)
    expect(wf.name).toBe('Flow')
    expect(wf.labels[0].name).toBe('a')
  })

  it('ignores unknown top-level keys (forward compatible)', () => {
    const yaml = 'name: Flow\nversion: 2\nlabels:\n  - name: a\n    sort_order: 0\n'
    const wf = parseWorkflowYaml(yaml)
    expect(wf.name).toBe('Flow')
    expect(wf.labels).toHaveLength(1)
  })

  it('throws on empty input', () => {
    expect(() => parseWorkflowYaml('')).toThrow(/empty/)
    expect(() => parseWorkflowYaml('   \n')).toThrow(/empty/)
  })

  it('throws when the required name field is missing', () => {
    expect(() => parseWorkflowYaml('labels:\n  - name: a\n    sort_order: 0\n')).toThrow(/missing required "name"/)
  })

  it('throws when a label entry has no name', () => {
    const yaml = 'name: Flow\nlabels:\n  - sort_order: 0\n'
    expect(() => parseWorkflowYaml(yaml)).toThrow(/label entry is missing a "name"/)
  })

  it('throws when a transition entry is missing from/to', () => {
    const yaml = 'name: Flow\ntransitions:\n  - from: a\n'
    expect(() => parseWorkflowYaml(yaml)).toThrow(/missing "from" or "to"/)
  })

  it('throws on a top-level line without a colon', () => {
    expect(() => parseWorkflowYaml('name: Flow\nbogus line\n')).toThrow(/expected "key: value" at top level/)
  })
})
