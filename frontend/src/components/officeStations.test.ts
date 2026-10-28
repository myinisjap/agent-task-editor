import { describe, it, expect } from 'vitest'
import { officeStationsFor } from './officeStations'
import type { Workflow } from '../api/client'

function label(overrides: Partial<Workflow['labels'][number]>): Workflow['labels'][number] {
  return {
    id: overrides.name ?? 'id', workflow_id: 'wf', name: 'name', color: '#000', sort_order: 0,
    agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit: null, wip_limit_hard: 0,
    verify_mode: 'inherit', verify_command: '', ...overrides,
  }
}
function workflow(labels: Workflow['labels'], transitions: Workflow['transitions'] = []): Workflow {
  return { id: 'wf', name: 'wf', description: '', labels, transitions, created_at: '', updated_at: '' }
}
const tr = (from: string, to: string, trigger_type: 'human' | 'agent'): Workflow['transitions'][number] =>
  ({ id: `${from}-${to}`, workflow_id: 'wf', from_label: from, to_label: to, trigger_type })

const NAMES = ['not_ready', 'plan', 'review-plan', 'work', 'testing', 'agent-review', 'review', 'done']

describe('officeStationsFor gates', () => {
  it('flags default-workflow human gates but not the agent_ignore backlog', () => {
    const wf = workflow(
      NAMES.map((name, i) => label({ name, sort_order: i, agent_ignore: name === 'not_ready' ? 1 : 0, is_terminal: name === 'done' ? 1 : 0 })),
      [
        tr('plan', 'review-plan', 'agent'), tr('review-plan', 'work', 'human'),
        tr('work', 'testing', 'agent'), tr('review', 'done', 'human'),
        tr('not_ready', 'plan', 'human'),
      ],
    )
    const by = Object.fromEntries(officeStationsFor(wf, {}).map((s) => [s.name, s]))
    expect(by['review-plan'].gate).toBe(true)
    expect(by['review'].gate).toBe(true)
    expect(by['not_ready'].gate).toBe(false)
    expect(by['work'].gate).toBe(false)
    expect(by['done'].gate).toBe(false)
  })

  it('only the waitingHuman bucket is a gate for custom workflows', () => {
    const wf = workflow(
      [label({ name: 'todo', agent_ignore: 1 }), label({ name: 'doing' }), label({ name: 'review' }), label({ name: 'shipped', is_terminal: 1 })],
      [tr('doing', 'review', 'agent')],
    )
    const s = officeStationsFor(wf, { review: 2 })
    expect(s.map((x) => [x.key, !!x.gate])).toEqual([['notReady', false], ['agentWorking', false], ['waitingHuman', true]])
    expect(s[2].count).toBe(2)
  })
})
