import { describe, expect, it } from 'vitest'
import { pickClip, recolorShirt, recolorWorker, classifyWorkerPixel, rgbToHsl, WORKER_SHEET, ROBOT_SHEET, type SheetSpec } from './officeSheets'
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

function cls(hex: string, y: number, neck = 20, cellH = 48): number {
  const n = parseInt(hex.slice(1), 16)
  const [h, s, l] = rgbToHsl((n >> 16) & 255, (n >> 8) & 255, n & 255)
  return classifyWorkerPixel(h, s, l, y, neck, cellH)
}

describe('classifyWorkerPixel', () => {
  it('splits shirt, hair, skin and leaves everything else', () => {
    expect(cls('#0c8384', 25)).toBe(1)
    expect(cls('#68371e', 5)).toBe(2)
    expect(cls('#7a4f3c', 5)).toBe(2)
    expect(cls('#68371e', 20)).toBe(0) // boots share the hair brown
    expect(cls('#68371e', 40)).toBe(0)
    expect(cls('#f5b289', 8)).toBe(3)
    expect(cls('#924e46', 10)).toBe(3)
    for (const c of ['#1e1b2e', '#19283f', '#1e0b2a', '#d4a064', '#b07a45', '#d6ad8e', '#db9762'])
      expect(cls(c, 8)).toBe(0)
  })
})

describe('recolorWorker', () => {
  const W = 8, H = 12
  const hex = (h: string): number[] => { const n = parseInt(h.slice(1), 16); return [(n >> 16) & 255, (n >> 8) & 255, n & 255, 255] }
  const build = (): Uint8ClampedArray => {
    const px: number[] = []
    for (let x = 0; x < W; x++) px.push(...hex('#68371e')) // hair
    for (let x = 0; x < W; x++) px.push(...hex('#f5b289')) // skin
    for (let x = 0; x < W; x++) px.push(...hex('#0c8384')) // shirt (neck)
    for (let x = 0; x < W; x++) px.push(...hex('#68371e')) // belt/boot brown
    for (let x = 0; x < W * 8; x++) px.push(...hex('#19283f'))
    return new Uint8ClampedArray(px)
  }
  it('recolours hair, skin and shirt in one pass and leaves boots alone', () => {
    const px = build()
    const ref = build()
    recolorWorker(px, W, H, { T: '#e05a4e', r: '#1c1310', K: '#6f4426' }, W, H)
    const lum = (a: Uint8ClampedArray, i: number) => a[i] + a[i + 1] + a[i + 2]
    expect(lum(px, 0)).toBeLessThan(lum(ref, 0))
    expect(lum(px, W * 4)).toBeLessThan(lum(ref, W * 4))
    expect(Array.from(px.slice(W * 12, W * 16))).toEqual(Array.from(ref.slice(W * 12, W * 16)))
    const only = build()
    recolorShirt(only, '#e05a4e')
    expect(Array.from(px.slice(W * 8, W * 12))).toEqual(Array.from(only.slice(W * 8, W * 12)))
  })
  it('leaves skin and shirt alone when only hair is given', () => {
    const px = build()
    recolorWorker(px, W, H, { r: '#1c1310' }, W, H)
    const ref = build()
    expect(Array.from(px.slice(W * 4))).toEqual(Array.from(ref.slice(W * 4)))
  })
})
