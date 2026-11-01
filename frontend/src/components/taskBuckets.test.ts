import { describe, it, expect } from 'vitest'
import { bucketize } from './taskBuckets'
import {
  composeFrame,
  actionFrameCount,
  actionFrameDur,
  actionBob,
  isImpactFrame,
  ROBOT_HOVER_BOB,
  SHADOW_ROWS,
  SHADOW_ALPHA,
  WALK_FRAME_COUNT,
  GRID_W,
  GRID_H,
  type Action,
  type Dir,
} from './pixelSprites'
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
    wip_limit: null,
    wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '',
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
        for (const leg of [-1, ...Array.from({ length: WALK_FRAME_COUNT }, (_, i) => i)]) {
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

  it('overlays walk legs on a humanoid and flares the robot thruster', () => {
    const still = composeFrame('idle', 0, -1)
    const walking = composeFrame('idle', 0, 0)
    expect(walking.rows).not.toEqual(still.rows) // legs shifted

    // the robot hovers: thruster rows 20-23 react to walkLeg, the head does not
    const rWalk = composeFrame('robot', 0, 0).rows
    const rPark = composeFrame('robot', 0, -1).rows
    expect(rWalk.slice(20)).not.toEqual(rPark.slice(20))
    expect(rWalk.slice(0, 11)).toEqual(rPark.slice(0, 11))
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

  const ACTIONS: Action[] = ['idle', 'drawing', 'inspecting', 'hammering', 'testing', 'robot', 'approving', 'celebrating', 'waving']
  const wellFormed = (f: { rows: string[]; palette: Record<string, string> }) => {
    expect(f.rows).toHaveLength(GRID_H)
    for (const row of f.rows) {
      expect(row).toHaveLength(GRID_W)
      for (const ch of row) if (ch !== '.' && ch !== ' ') expect(typeof f.palette[ch]).toBe('string')
    }
  }

  it('has a >=4 frame walk cycle and >=6 frame action loops', () => {
    expect(WALK_FRAME_COUNT).toBeGreaterThanOrEqual(4)
    for (const a of ACTIONS) expect(actionFrameCount(a)).toBeGreaterThanOrEqual(6)
  })

  it('walk frames, back-work frames, blink and breath are well-formed (human + robot)', () => {
    for (const robot of [false, true]) {
      for (const dir of ['front', 'side', 'back'] as const) {
        for (let leg = 0; leg < WALK_FRAME_COUNT; leg++) wellFormed(composeFrame('idle', 0, leg, undefined, dir, robot))
        for (const a of ACTIONS.filter((x) => x !== 'robot')) {
          for (let i = 0; i < 8; i++) {
            for (const opts of [{}, { blink: true }, { breath: true }, { blink: true, breath: true }]) {
              wellFormed(composeFrame(a, i, -1, undefined, dir, robot, opts))
            }
          }
        }
      }
    }
  })

  it('back-view work frames differ from the plain back body', () => {
    const plainBack = composeFrame('hammering', 0, 0, undefined, 'back').rows // walking back view differs only in legs
    for (const a of ACTIONS.filter((x) => x !== 'robot' && x !== 'idle')) {
      const frames = [0, 1, 2, 3].map((i) => composeFrame(a, i, -1, undefined, 'back').rows.join('\n'))
      expect(new Set(frames).size).toBeGreaterThan(1) // animated
      expect(frames.some((f) => f !== plainBack.join('\n'))).toBe(true)
    }
  })

  it('blink closes the eyes and breath shifts the head', () => {
    const open = composeFrame('idle', 0)
    const shut = composeFrame('idle', 0, -1, undefined, 'front', false, { blink: true })
    expect(open.rows[6]).toContain('e')
    expect(shut.rows[6]).not.toContain('e')
    const breath = composeFrame('idle', 0, -1, undefined, 'front', false, { breath: true })
    expect(breath.rows.slice(0, 11)).not.toEqual(open.rows.slice(0, 11))
    expect(breath.rows.slice(11)).toEqual(open.rows.slice(11))
  })

  it('keeps raised tools visible in robot mode (head no longer erases them)', () => {
    const robot = composeFrame('hammering', 2, -1, undefined, 'front', true) // peak of the swing
    expect(robot.rows.slice(0, 8).some((r) => r.includes('m'))).toBe(true)
    expect(robot.palette.m).toBeTruthy()
  })

  it('has a positive per-action frame duration', () => {
    for (const a of ACTIONS) expect(actionFrameDur(a)).toBeGreaterThan(0)
  })

  const DIRS: Dir[] = ['front', 'side', 'back']

  it('robot action has distinct, animated front/side/back views', () => {
    const [front, side, back] = DIRS.map((d) => composeFrame('robot', 0, -1, undefined, d))
    expect(front.rows).not.toEqual(side.rows)
    expect(front.rows).not.toEqual(back.rows)
    expect(side.rows).not.toEqual(back.rows)
    const mirror = (rows: string[]) => rows.map((r) => [...r].reverse().join(''))
    expect(side.rows).not.toEqual(mirror(side.rows)) // profile is asymmetric
    const backFrames = Array.from({ length: 8 }, (_, i) => JSON.stringify(composeFrame('robot', i, -1, undefined, 'back')))
    expect(new Set(backFrames).size).toBeGreaterThan(2) // LEDs blink
    expect(actionFrameCount('robot')).toBeGreaterThanOrEqual(6)
    expect(ROBOT_HOVER_BOB).toHaveLength(WALK_FRAME_COUNT)
    expect(actionBob('robot', 2)).toBeLessThan(0) // parked hover
  })

  it('robot frames are well-formed for every dir, leg and carry state', () => {
    for (const dir of DIRS) {
      for (let leg = -1; leg < WALK_FRAME_COUNT; leg++) {
        for (const carry of [false, true]) {
          for (let i = 0; i < 8; i++) wellFormed(composeFrame('robot', i, leg, undefined, dir, false, { carry }))
        }
      }
    }
  })

  it('side-view tooled actions have several distinct frames, well-formed for human and robot', () => {
    for (const a of ACTIONS.filter((x) => x !== 'robot' && x !== 'idle')) {
      const grids = [0, 1, 2, 3].map((i) => composeFrame(a, i, -1, undefined, 'side').rows.join('\n'))
      expect(new Set(grids).size).toBeGreaterThan(1)
      for (const robot of [false, true]) for (let i = 0; i < 8; i++) wellFormed(composeFrame(a, i, -1, undefined, 'side', robot))
    }
  })

  it('carrying draws a task box for every dir (humans and the robot sprite)', () => {
    for (const dir of DIRS) {
      for (const robot of [false, true]) {
        const plain = composeFrame('idle', 0, 1, undefined, dir, robot)
        const carry = composeFrame('idle', 0, 1, undefined, dir, robot, { carry: true })
        expect(carry.rows.join('')).toContain('w')
        expect(carry.rows).not.toEqual(plain.rows)
        wellFormed(carry)
      }
      const rp = composeFrame('robot', 0, 1, undefined, dir)
      const rc = composeFrame('robot', 0, 1, undefined, dir, false, { carry: true })
      expect(rc.rows.join('')).toContain('w')
      expect(rc.rows).not.toEqual(rp.rows)
    }
  })

  it('flags impact frames per dir (never frame 0)', () => {
    for (const dir of DIRS) {
      expect(isImpactFrame('hammering', 0, dir)).toBe(false)
      expect(Array.from({ length: 8 }, (_, i) => isImpactFrame('hammering', i, dir)).some(Boolean)).toBe(true)
      expect(Array.from({ length: 8 }, (_, i) => isImpactFrame('celebrating', i, dir)).some(Boolean)).toBe(true)
    }
    expect(isImpactFrame('drawing', 3, 'front')).toBe(false)
  })

  it('shadow tables shrink and fade with lift', () => {
    expect(SHADOW_ROWS.length).toBe(SHADOW_ALPHA.length)
    const area = SHADOW_ROWS.map((r) => r.reduce((a, b) => a + b, 0))
    expect(area[1]).toBeLessThan(area[0])
    expect(area[2]).toBeLessThan(area[1])
    expect(SHADOW_ALPHA[2]).toBeLessThan(SHADOW_ALPHA[0])
  })
})
