import { describe, it, expect } from 'vitest'
import type { AgentConfig, Workflow } from '../api/client'
import { labelStaffing, unstaffedLabelNames, toggleAgentLabel, workflowStaffing } from './staffing'

const lbl = (name: string, extra: Record<string, unknown> = {}, i = 0) =>
  ({ id: name, workflow_id: 'w', name, color: '', sort_order: i, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '', ...extra }) as Workflow['labels'][number]
const tr = (from: string, to: string, trigger: string, extra: Record<string, unknown> = {}) =>
  ({ id: `${from}-${to}`, workflow_id: 'w', from_label: from, to_label: to, trigger_type: trigger, ...extra }) as Workflow['transitions'][number]
const agent = (id: string, labels: string[], enabled = true) =>
  ({ id, name: id, labels: JSON.stringify(labels), enabled }) as AgentConfig

const wf = {
  id: 'w', name: 'W', description: '',
  labels: [lbl('work', {}, 0), lbl('review', {}, 1), lbl('hold', { agent_ignore: 1 }, 2), lbl('done', { is_terminal: 1 }, 3)],
  transitions: [tr('work', 'review', 'agent'), tr('review', 'done', 'both'), tr('hold', 'work', 'human')],
} as unknown as Workflow

describe('labelStaffing', () => {
  it('classifies each label kind', () => {
    const agents = [agent('coder', ['work'])]
    const get = (n: string) => labelStaffing(wf, agents, wf.labels.find((l) => l.name === n)!)
    expect(get('work').kind).toBe('agent')
    expect(get('work').agents.map((a) => a.id)).toEqual(['coder'])
    expect(get('review').kind).toBe('unstaffed')
    expect(get('hold').kind).toBe('human')
    expect(get('done').kind).toBe('terminal')
  })

  it('does not count disabled agents as staffing but still lists them', () => {
    const s = labelStaffing(wf, [agent('off', ['work'], false)], wf.labels[0])
    expect(s.kind).toBe('unstaffed')
    expect(s.agents).toHaveLength(1)
  })

  it('counts an explicit transition agent assignment', () => {
    const w = { ...wf, transitions: [tr('work', 'review', 'agent', { agent_config_id: 'x' }), ...wf.transitions.slice(1)] } as Workflow
    expect(labelStaffing(w, [agent('x', [])], w.labels[0]).kind).toBe('agent')
  })
})

describe('unstaffedLabelNames / workflowStaffing', () => {
  it('returns only unstaffed agent labels', () => {
    expect([...unstaffedLabelNames(wf, [agent('coder', ['work'])])]).toEqual(['review'])
    expect([...unstaffedLabelNames(wf, [])]).toEqual(['work', 'review'])
    expect(unstaffedLabelNames(undefined, []).size).toBe(0)
  })
  it('covers every label', () => {
    expect(workflowStaffing(wf, []).size).toBe(4)
  })
})

describe('toggleAgentLabel', () => {
  it('adds without duplicating and removes', () => {
    const a = agent('a', ['work'])
    expect(JSON.parse(toggleAgentLabel(a, 'review', true))).toEqual(['work', 'review'])
    expect(JSON.parse(toggleAgentLabel(a, 'work', true))).toEqual(['work'])
    expect(JSON.parse(toggleAgentLabel(a, 'work', false))).toEqual([])
  })
})
