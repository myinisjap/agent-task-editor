import { describe, expect, it } from 'vitest'
import { pickClip, recolorShirt, WORKER_SHEET, ROBOT_SHEET, type SheetSpec } from './officeSheets'
import type { Action, Dir } from './pixelSprites'

const ACTIONS: Action[] = ['idle', 'drawing', 'inspecting', 'hammering', 'testing', 'robot', 'approving', 'celebrating', 'waving']
const DIRS: Dir[] = ['front', 'side', 'back']

describe('pickClip', () => {
  it('returns a real row for every action, direction, motion and crew combination', () => {
    for (const action of ACTIONS)
      for (const dir of DIRS)
        for (const moving of [false, true])
          for (const carry of [false, true])
            for (const robots of [false, true]) {
              const c = pickClip({ action, dir, moving, carry, robots })
              const spec: SheetSpec = c.sheet === 'worker' ? WORKER_SHEET : ROBOT_SHEET
              expect(spec.rows[c.name]).toEqual([c.row, c.frames])
              expect(c.frames).toBeGreaterThan(0)
            }
  })

  it('walks the worker in the direction of travel, carrying a box on hand-offs', () => {
    expect(pickClip({ action: 'hammering', dir: 'side', moving: true, carry: false, robots: false }).name).toBe('walk_east')
    expect(pickClip({ action: 'hammering', dir: 'back', moving: true, carry: true, robots: false }).name).toBe('carrying_north')
  })

  it('plays the station action facing the workstation', () => {
    expect(pickClip({ action: 'drawing', dir: 'back', moving: false, carry: false, robots: false }).name).toBe('drawing_north')
    expect(pickClip({ action: 'approving', dir: 'side', moving: false, carry: false, robots: false }).name).toBe('approving_east')
    expect(pickClip({ action: 'waving', dir: 'back', moving: false, carry: false, robots: false }).name).toBe('wave_south')
  })

  it('uses the robot sheet for the robot crew and the agent-review station', () => {
    expect(pickClip({ action: 'robot', dir: 'back', moving: false, carry: false, robots: false })).toMatchObject({ sheet: 'robot', name: 'working_north' })
    expect(pickClip({ action: 'testing', dir: 'side', moving: true, carry: true, robots: true })).toMatchObject({ sheet: 'robot', name: 'carrying_east' })
    expect(pickClip({ action: 'testing', dir: 'front', moving: true, carry: false, robots: true }).name).toBe('fly_south')
  })
})

describe('recolorShirt', () => {
  it('recolours teal shirt pixels and leaves skin, outline and transparent pixels alone', () => {
    const px = new Uint8ClampedArray([
      0x0c, 0x83, 0x84, 255, // shirt teal
      0xf5, 0xb2, 0x89, 255, // skin
      0x1e, 0x1b, 0x2e, 255, // outline
      0x0c, 0x83, 0x84, 0, // transparent
    ])
    recolorShirt(px, '#e05a4e')
    const [r, g, b] = px.slice(0, 3)
    expect(r).toBeGreaterThan(g)
    expect(r).toBeGreaterThan(b)
    expect(Array.from(px.slice(4))).toEqual([0xf5, 0xb2, 0x89, 255, 0x1e, 0x1b, 0x2e, 255, 0x0c, 0x83, 0x84, 0])
  })
})
