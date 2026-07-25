import { describe, it, expect } from 'vitest'
import { stationsFor } from './factoryStations'
import { machineSvg, itemSvg, DEFAULT_FACTORY_ACTIONS, type FactoryAction } from './factoryMachines'
import type { Workflow } from '../api/client'

function label(overrides: Partial<Workflow['labels'][number]>): Workflow['labels'][number] {
  return {
    id: overrides.name ?? 'id',
    workflow_id: 'wf',
    name: 'name',
    color: '#000',
    sort_order: 0,
    agent_ignore: 0,
    is_terminal: 0,
    create_pr: 0,
    ...overrides,
  }
}

function workflow(labels: Workflow['labels'], transitions: Workflow['transitions'] = []): Workflow {
  return { id: 'wf', name: 'wf', description: '', labels, transitions, created_at: '', updated_at: '' }
}

const DEFAULT_NAMES = ['not_ready', 'plan', 'review-plan', 'work', 'testing', 'agent-review', 'review', 'done']

describe('stationsFor', () => {
  it('maps the seeded default workflow to one bespoke machine per label, in sort order', () => {
    const wf = workflow(
      DEFAULT_NAMES.map((name, i) => label({ name, sort_order: DEFAULT_NAMES.length - i, is_terminal: name === 'done' ? 1 : 0 })),
    )
    const counts = { work: 4, done: 2 }
    const stations = stationsFor(wf, counts)

    // sorted by sort_order (reversed above), so 'done' (sort_order 1) comes first
    expect(stations.map((s) => s.name)).toEqual([...DEFAULT_NAMES].reverse())
    expect(stations.find((s) => s.name === 'work')!.action).toBe('hammering')
    expect(stations.find((s) => s.name === 'agent-review')!.action).toBe('robot')
    expect(stations.find((s) => s.name === 'done')!.action).toBe('packing')
    expect(stations.find((s) => s.name === 'work')!.count).toBe(4)
    expect(stations.find((s) => s.name === 'plan')!.count).toBe(0)
  })

  it('collapses a custom workflow into the 3 buckets with summed counts', () => {
    const wf = workflow(
      [
        label({ name: 'todo', agent_ignore: 1 }),
        label({ name: 'doing' }),
        label({ name: 'review' }),
        label({ name: 'shipped', is_terminal: 1 }),
      ],
      [{ id: 't1', workflow_id: 'wf', from_label: 'doing', to_label: 'review', trigger_type: 'agent' }],
    )
    const stations = stationsFor(wf, { todo: 3, doing: 1, review: 2, shipped: 5 })

    expect(stations.map((s) => s.key)).toEqual(['notReady', 'agentWorking', 'waitingHuman'])
    expect(stations.map((s) => s.action)).toEqual(['idle', 'robot', 'approving'])
    expect(stations.find((s) => s.key === 'notReady')!.count).toBe(3)
    expect(stations.find((s) => s.key === 'agentWorking')!.count).toBe(1)
    expect(stations.find((s) => s.key === 'waitingHuman')!.count).toBe(2)
  })
})

describe('machineSvg / itemSvg', () => {
  it('renders a single <svg> for every factory action', () => {
    const actions = [...new Set(Object.values(DEFAULT_FACTORY_ACTIONS))] as FactoryAction[]
    for (const action of actions) {
      const svg = machineSvg(action, '#8B5CF6')
      expect(svg.startsWith('<svg')).toBe(true)
      expect(svg.match(/<svg/g)).toHaveLength(1)
      expect(svg).toContain('#8B5CF6') // accent threaded through
    }
  })

  it('accretes markup as the part advances through the build stages', () => {
    let prev = itemSvg(0, '#10B981').length
    for (let stage = 1; stage <= 6; stage++) {
      const len = itemSvg(stage, '#10B981').length
      expect(len).toBeGreaterThan(prev)
      prev = len
    }
  })

  it('falls back to the intake machine for an unknown action', () => {
    expect(machineSvg('nope' as FactoryAction, '#fff')).toBe(machineSvg('idle', '#fff'))
  })
})
