import { describe, it, expect } from 'vitest'
import { officeStationsFor, sceneTaskStatus, shippedBoxes, type SceneTask } from './officeStations'
import type { Task, Workflow } from '../api/client'

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

describe('officeStationsFor tasks', () => {
  const wf = workflow(
    NAMES.map((name, i) => label({ name, sort_order: i, agent_ignore: name === 'not_ready' ? 1 : 0, is_terminal: name === 'done' ? 1 : 0 })),
  )
  const task = (id: string, lbl: string, extra: Partial<Task> = {}): Task =>
    ({ id, title: `T ${id}`, type: 'feature', label: lbl, workflow_id: 'wf', ...extra }) as Task
  const by = (st: ReturnType<typeof officeStationsFor>) => Object.fromEntries(st.map((s) => [s.name, s]))

  it('is unchanged without tasks', () => {
    const st = officeStationsFor(wf, { work: 2 })
    expect(st.every((s) => s.tasks === undefined && s.terminal === undefined)).toBe(true)
  })
  it('maps tasks to label stations, dropping other workflows and archived', () => {
    const st = by(officeStationsFor(wf, { work: 2 }, [
      task('a', 'work'), task('b', 'work'), task('c', 'plan'),
      task('x', 'work', { workflow_id: 'other' }), task('y', 'work', { archived: true }),
    ]))
    expect(st.work.tasks!.map((t) => t.id)).toEqual(['a', 'b'])
    expect(st.plan.tasks!.map((t) => t.id)).toEqual(['c'])
    expect(st.testing.tasks).toEqual([])
    expect(st.done.terminal).toBe(true)
    expect(st.work.terminal).toBe(false)
    expect(st.work.count).toBe(2)
  })
  it('maps tasks to buckets in a custom workflow and drops terminal labels', () => {
    const custom = workflow(
      [label({ name: 'todo', sort_order: 0, agent_ignore: 1 }), label({ name: 'build', sort_order: 1 }), label({ name: 'ship', sort_order: 2, is_terminal: 1 })],
      [tr('build', 'ship', 'agent')],
    )
    const st = by(officeStationsFor(custom, {}, [task('a', 'todo'), task('b', 'build'), task('c', 'ship')]))
    expect(st['Not ready'].tasks!.map((t) => t.id)).toEqual(['a'])
    expect(st['Agent working'].tasks!.map((t) => t.id)).toEqual(['b'])
    expect(st['Waiting on human'].tasks).toEqual([])
    expect(st['Agent working'].terminal).toBe(false)
  })
  it('sorts busy first, then rate limited, then the queue order', () => {
    const st = by(officeStationsFor(wf, {}, [
      task('q2', 'work', { queue_position: 2 }),
      task('q1', 'work', { queue_position: 1 }),
      task('rl', 'work', { block_reason: { code: 'rate_limited', message: '' } }),
      task('run', 'work', { active_run_status: 'running' }),
    ]))
    expect(st.work.tasks!.map((t) => t.id)).toEqual(['run', 'rl', 'q1', 'q2'])
  })
  it('derives rateLimited from block_reason and the WS map, ignoring expired entries', () => {
    const now = Date.parse('2030-01-01T12:00:00Z')
    const future = '2030-01-01T13:00:00Z'
    const past = '2030-01-01T11:00:00Z'
    const st = by(officeStationsFor(
      wf, {},
      [
        task('a', 'work', { block_reason: { code: 'rate_limited', message: '', clears_at: future } }),
        task('b', 'work', { block_reason: { code: 'rate_limited', message: '', clears_at: past } }),
        task('c', 'work'), task('d', 'work'), task('e', 'work', { block_reason: { code: 'paused', message: '' } }),
      ],
      { now, rateLimitedUntil: { c: future, d: past } },
    ))
    const rl = Object.fromEntries(st.work.tasks!.map((t) => [t.id, t.rateLimited]))
    expect(rl).toEqual({ a: true, b: false, c: true, d: false, e: false })
  })
})

describe('sceneTaskStatus / shippedBoxes', () => {
  const t = (extra: Partial<SceneTask> = {}): SceneTask => ({ id: 'a', title: 'A', type: 'bug', order: 0, rateLimited: false, ...extra })
  const fmt = () => '14:05'
  it('describes each state', () => {
    expect(sceneTaskStatus(t({ rateLimited: true, clearsAt: '2030-01-01T14:05:00Z' }), false, fmt)).toBe('rate limited until 14:05')
    expect(sceneTaskStatus(t({ rateLimited: true }), false, fmt)).toBe('rate limited')
    expect(sceneTaskStatus(t({ runStatus: 'running' }), false)).toBe('running')
    expect(sceneTaskStatus(t({ runStatus: 'pending' }), false)).toBe('starting')
    expect(sceneTaskStatus(t({ runStatus: 'waiting_human' }), false)).toBe('waiting on you')
    expect(sceneTaskStatus(t(), true)).toBe('waiting on you')
    expect(sceneTaskStatus(t({ blockCode: 'wip_limit' }), false)).toBe('wip limit reached')
    expect(sceneTaskStatus(t(), false)).toBe('queued')
  })
  it('grows the shipped pile from 0 to 5', () => {
    expect([0, 1, 2, 3, 6, 10, 15, 99].map(shippedBoxes)).toEqual([0, 1, 1, 2, 3, 4, 5, 5])
  })
})
