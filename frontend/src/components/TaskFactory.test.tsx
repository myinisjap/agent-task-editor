import { describe, it, expect } from 'vitest'
import { bucketize } from './taskBuckets'
import { composeFrame, actionFrameCount, GRID_W, GRID_H } from './pixelSprites'
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

describe('bucketize', () => {
  it('returns null for the seeded default 8-label workflow', () => {
    const names = ['not_ready', 'plan', 'review-plan', 'work', 'testing', 'agent-review', 'review', 'done']
    const wf = workflow(names.map((name, i) => label({ name, sort_order: i, is_terminal: name === 'done' ? 1 : 0 })))
    expect(bucketize(wf)).toBeNull()
  })

  it('groups a custom workflow into 3 buckets, excludes terminal, agent_ignore wins', () => {
    const wf = workflow(
      [
        label({ name: 'todo', agent_ignore: 1 }), // notReady
        label({ name: 'doing' }), // from_label of an 'agent' transition -> agentWorking
        label({ name: 'blocked', agent_ignore: 1 }), // is also a from_label but agent_ignore wins -> notReady
        label({ name: 'review' }), // waitingHuman (not terminal, no agent transition)
        label({ name: 'shipped', is_terminal: 1 }), // terminal, excluded entirely
      ],
      [
        { id: 't1', workflow_id: 'wf', from_label: 'doing', to_label: 'review', trigger_type: 'agent' },
        { id: 't2', workflow_id: 'wf', from_label: 'blocked', to_label: 'doing', trigger_type: 'both' },
      ],
    )

    const result = bucketize(wf)
    expect(result).not.toBeNull()
    expect(result!.notReady.sort()).toEqual(['blocked', 'todo'])
    expect(result!.agentWorking).toEqual(['doing'])
    expect(result!.waitingHuman).toEqual(['review'])
    // 'shipped' (terminal) must not appear in any bucket
    expect([...result!.notReady, ...result!.agentWorking, ...result!.waitingHuman]).not.toContain('shipped')
  })
})

describe('composeFrame', () => {
  it('returns a well-formed grid for every action frame', () => {
    const actions = ['idle', 'drawing', 'inspecting', 'hammering', 'testing', 'robot', 'approving', 'celebrating', 'waving'] as const
    for (const action of actions) {
      for (let i = 0; i < actionFrameCount(action); i++) {
        const f = composeFrame(action, i)
        expect(f.rows).toHaveLength(GRID_H)
        expect(f.rows.every((r) => r.length === GRID_W)).toBe(true)
        // every non-transparent cell must resolve to a color
        for (const row of f.rows) {
          for (const ch of row) {
            if (ch !== '.' && ch !== ' ') expect(typeof f.palette[ch]).toBe('string')
          }
        }
      }
    }
  })

  it('renders a well-formed grid in robot mode across actions/dirs/walk', () => {
    const actions = ['idle', 'drawing', 'hammering', 'testing', 'approving', 'celebrating', 'waving'] as const
    const dirs = ['front', 'side', 'back'] as const
    for (const action of actions) {
      for (const dir of dirs) {
        for (const leg of [-1, 0, 1, 2]) {
          const f = composeFrame(action, 0, leg, undefined, dir, true) // robot = true
          expect(f.rows).toHaveLength(GRID_H)
          expect(f.rows.every((r) => r.length === GRID_W)).toBe(true)
          for (const row of f.rows) {
            for (const ch of row) {
              if (ch !== '.' && ch !== ' ') expect(typeof f.palette[ch]).toBe('string')
            }
          }
        }
      }
    }
  })

  it('robot mode changes the head vs. the human sprite', () => {
    const human = composeFrame('idle', 0, -1, undefined, 'front', false)
    const robot = composeFrame('idle', 0, -1, undefined, 'front', true)
    expect(robot.rows.slice(0, 10)).not.toEqual(human.rows.slice(0, 10)) // head reshaped
  })

  it('overlays walk legs on a humanoid but not on the robot', () => {
    const still = composeFrame('idle', 0, -1)
    const walking = composeFrame('idle', 0, 0)
    expect(walking.rows).not.toEqual(still.rows) // legs shifted

    // robot ignores walkLeg (rolls on treads) — identical with or without it
    expect(composeFrame('robot', 0, 0).rows).toEqual(composeFrame('robot', 0, -1).rows)
  })

  it('wraps out-of-range frame indices instead of crashing', () => {
    expect(() => composeFrame('hammering', 99)).not.toThrow()
    expect(composeFrame('hammering', actionFrameCount('hammering')).rows).toEqual(composeFrame('hammering', 0).rows)
  })

  it('recolors a humanoid via a variant but leaves the robot untouched', () => {
    const plain = composeFrame('idle', 0)
    const green = composeFrame('idle', 0, -1, { h: '#00ff00', T: '#123456' })
    expect(green.rows).toEqual(plain.rows) // shape identical
    expect(green.palette.h).toBe('#00ff00') // hat recolored
    expect(green.palette.T).toBe('#123456') // shirt recolored
    expect(green.palette.B).toBe(plain.palette.B) // non-variant char untouched

    const robot = composeFrame('robot', 0, -1, { h: '#00ff00' })
    expect(robot.palette.h).toBeUndefined() // robot has no variant keys
  })
})
