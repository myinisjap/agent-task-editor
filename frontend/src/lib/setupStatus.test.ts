import { describe, it, expect } from 'vitest'
import {
  agentTriggerableLabels,
  computeSetup,
  parseAgentLabels,
  planStaffing,
  uncoveredAgentLabels,
} from './setupStatus'
import { defaultWorkflow, agentFor } from './__fixtures__/setupFixtures'

describe('agentTriggerableLabels', () => {
  it('returns the default workflow labels an agent works, in sort order', () => {
    expect(agentTriggerableLabels(defaultWorkflow())).toEqual(['plan', 'work', 'testing', 'agent-review'])
  })

  it('excludes agent_ignore labels and human-only sources', () => {
    const wf = defaultWorkflow()
    wf.labels = wf.labels.map((l) => (l.name === 'work' ? { ...l, agent_ignore: 1 } : l))
    expect(agentTriggerableLabels(wf)).toEqual(['plan', 'testing', 'agent-review'])
  })

  it('includes labels whose transition is "both"', () => {
    const wf = defaultWorkflow()
    wf.transitions = wf.transitions.map((t) => (t.from_label === 'review' ? { ...t, trigger_type: 'both' } : t))
    expect(agentTriggerableLabels(wf)).toContain('review')
  })
})

describe('uncoveredAgentLabels', () => {
  const wf = defaultWorkflow()

  it('reports every agent label when there are no agents', () => {
    expect(uncoveredAgentLabels([wf], [])).toEqual(['plan', 'work', 'testing', 'agent-review'])
  })

  it('one agent is not enough', () => {
    expect(uncoveredAgentLabels([wf], agentFor(['plan']))).toEqual(['work', 'testing', 'agent-review'])
  })

  it('is empty when all labels are covered', () => {
    expect(uncoveredAgentLabels([wf], agentFor(['plan', 'work', 'testing', 'agent-review']))).toEqual([])
  })

  it('ignores disabled agents', () => {
    expect(uncoveredAgentLabels([wf], agentFor(['plan', 'work', 'testing', 'agent-review'], false))).toHaveLength(4)
  })

  it('one agent can cover several labels', () => {
    const a = { ...agentFor(['plan'])[0], labels: '["plan","work","testing","agent-review"]' }
    expect(uncoveredAgentLabels([wf], [a])).toEqual([])
  })

  it('treats an explicit transition agent_config_id as coverage', () => {
    const w = defaultWorkflow()
    w.transitions = w.transitions.map((t) => (t.from_label === 'plan' ? { ...t, agent_config_id: 'x' } : t))
    expect(uncoveredAgentLabels([w], [])).not.toContain('plan')
  })

  it('checks every workflow and dedupes labels', () => {
    expect(uncoveredAgentLabels([wf, defaultWorkflow({ id: 'wf2' })], agentFor(['plan']))).toEqual([
      'work',
      'testing',
      'agent-review',
    ])
  })

  it('is empty when there are no workflows', () => {
    expect(uncoveredAgentLabels([], [])).toEqual([])
  })
})

describe('parseAgentLabels', () => {
  it('tolerates bad input', () => {
    expect(parseAgentLabels('nope')).toEqual([])
    expect(parseAgentLabels('{}')).toEqual([])
    expect(parseAgentLabels(undefined)).toEqual([])
    expect(parseAgentLabels('["a",1]')).toEqual(['a'])
  })
})

describe('planStaffing', () => {
  it('maps missing labels to the Planner/Worker/Tester/Reviewer templates', () => {
    const { templates, unmatched } = planStaffing(['plan', 'work', 'testing', 'agent-review'])
    expect(templates.map((t) => t.name)).toEqual(['Planner', 'Worker', 'Tester', 'Reviewer'])
    expect(unmatched).toEqual([])
  })

  it('only plans for missing labels and reports custom ones as unmatched', () => {
    const { templates, unmatched } = planStaffing(['testing', 'custom'])
    expect(templates.map((t) => t.name)).toEqual(['Tester'])
    expect(unmatched).toEqual(['custom'])
  })
})

describe('computeSetup', () => {
  const base = {
    repoCount: 1,
    providerCount: 1,
    taskCount: 1,
    workflows: [defaultWorkflow()],
    agents: agentFor(['plan', 'work', 'testing', 'agent-review']),
  }

  it('is complete when every step is done', () => {
    expect(computeSetup(base)).toMatchObject({ complete: true, staffed: true, missingAgentLabels: [] })
  })

  it('is incomplete with a partially staffed workflow', () => {
    const s = computeSetup({ ...base, agents: agentFor(['plan']) })
    expect(s.complete).toBe(false)
    expect(s.staffed).toBe(false)
    expect(s.missingAgentLabels).toEqual(['work', 'testing', 'agent-review'])
  })

  it.each([
    ['repo', { repoCount: 0 }],
    ['provider', { providerCount: 0 }],
    ['task', { taskCount: 0 }],
  ])('is incomplete without a %s', (_n, patch) => {
    expect(computeSetup({ ...base, ...patch }).complete).toBe(false)
  })
})
