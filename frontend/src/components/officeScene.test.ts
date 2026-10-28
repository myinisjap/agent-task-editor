import { describe, it, expect, vi, afterEach } from 'vitest'
import {
  OfficeScene,
  autoZoom,
  resolveZoom,
  columnsFor,
  pairHandoffs,
  ease,
  type Station,
} from './officeScene'

const mk = (key: string, count: number, extra: Partial<Station> = {}): Station => ({
  key, name: key, color: '#fff', action: 'idle', count, ...extra,
})
const stations = (counts: Record<string, number>, extra: Record<string, Partial<Station>> = {}) =>
  Object.entries(counts).map(([k, n]) => mk(k, n, extra[k]))

afterEach(() => vi.restoreAllMocks())

describe('autoZoom / resolveZoom', () => {
  it('picks the largest zoom that fits one row, capped at 6', () => {
    expect(autoZoom(2400, 8)).toBe(5)
    expect(autoZoom(4000, 8)).toBe(6)
    expect(autoZoom(2400, 8)).toBeLessThan(autoZoom(4000, 8))
  })
  it('never goes below the default zoom', () => {
    expect(autoZoom(375, 8)).toBe(3)
    expect(autoZoom(0, 8)).toBe(3)
  })
  it('uses the max zoom for few stations on a wide container', () => {
    expect(autoZoom(2000, 3)).toBe(6)
  })
  it('rounds and clamps numeric zoom', () => {
    expect(resolveZoom(4, 100, 8)).toBe(4)
    expect(resolveZoom(4.6, 100, 8)).toBe(5)
    expect(resolveZoom(0, 100, 8)).toBe(1)
    expect(resolveZoom(99, 100, 8)).toBe(6)
    expect(resolveZoom(NaN, 100, 8)).toBe(3)
    expect(resolveZoom('auto', 4000, 8)).toBe(6)
  })
})

describe('columnsFor', () => {
  it('fits fewer columns at higher zoom', () => {
    expect(columnsFor(1200, 6, 8)).toBeLessThan(columnsFor(1200, 3, 8))
  })
  it('is at least 1 and never more than n', () => {
    expect(columnsFor(10, 6, 8)).toBe(1)
    expect(columnsFor(100000, 3, 4)).toBe(4)
    expect(columnsFor(100000, 3, 0)).toBe(1)
  })
})

describe('ease', () => {
  it('is monotonic with fixed endpoints and slow ends', () => {
    expect(ease(0)).toBe(0)
    expect(ease(1)).toBe(1)
    expect(ease(0.5)).toBeCloseTo(0.5)
    expect(ease(0.1)).toBeLessThan(0.1)
    expect(ease(0.9)).toBeGreaterThan(0.9)
  })
})

describe('pairHandoffs', () => {
  it('pairs a single decrement with a single increment', () => {
    const r = pairHandoffs(stations({ a: 2, b: 1 }), stations({ a: 1, b: 2 }))
    expect(r).toEqual({ moves: [{ from: 'a', to: 'b' }], spawns: [], despawns: [] })
  })
  it('spawns an unpaired increment alongside a move', () => {
    const r = pairHandoffs(stations({ a: 2, b: 1, c: 0 }), stations({ a: 1, b: 2, c: 1 }))
    expect(r.moves).toEqual([{ from: 'a', to: 'b' }])
    expect(r.spawns).toEqual(['c'])
    expect(r.despawns).toEqual([])
  })
  it('despawns an unpaired decrement', () => {
    const r = pairHandoffs(stations({ a: 2, b: 1 }), stations({ a: 1, b: 1 }))
    expect(r).toEqual({ moves: [], spawns: [], despawns: ['a'] })
  })
  it('prefers a downstream destination', () => {
    const r = pairHandoffs(stations({ a: 0, b: 1, c: 0 }), stations({ a: 1, b: 0, c: 1 }))
    expect(r.moves).toEqual([{ from: 'b', to: 'c' }])
    expect(r.spawns).toEqual(['a'])
  })
  it('falls back to an upstream destination', () => {
    const r = pairHandoffs(stations({ a: 0, b: 1 }), stations({ a: 1, b: 0 }))
    expect(r.moves).toEqual([{ from: 'b', to: 'a' }])
  })
  it('handles multiple units', () => {
    const r = pairHandoffs(stations({ a: 3, b: 0 }), stations({ a: 1, b: 2 }))
    expect(r.moves).toEqual([{ from: 'a', to: 'b' }, { from: 'a', to: 'b' }])
  })
  it('returns nothing when counts are unchanged', () => {
    expect(pairHandoffs(stations({ a: 1, b: 2 }), stations({ a: 1, b: 2 }))).toEqual({ moves: [], spawns: [], despawns: [] })
  })
})

function crew(scene: OfficeScene) {
  const out: Record<string, number> = {}
  for (const s of scene.spriteStates()) out[s.stationKey] = (out[s.stationKey] ?? 0) + 1
  return out
}

describe('OfficeScene hand-offs', () => {
  it('walks a sprite between stations instead of rebuilding', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 2, b: 1, c: 0 }))
    expect(scene.spriteStates()).toHaveLength(3)
    scene.setStations(stations({ a: 1, b: 2, c: 0 }))
    const st = scene.spriteStates()
    expect(st).toHaveLength(3) // nobody vanished or was recreated
    const walkers = st.filter((s) => s.mode === 'handoff')
    expect(walkers).toHaveLength(1)
    expect(walkers[0].stationKey).toBe('b')
    for (let i = 0; i < 200; i++) scene.step(0.05)
    expect(scene.spriteStates().filter((s) => s.mode === 'handoff')).toHaveLength(0)
    expect(crew(scene)).toEqual({ a: 1, b: 2 })
  })

  it('walks a fresh sprite out of an overflowing source', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 8, b: 0 }))
    expect(crew(scene)).toEqual({ a: 6 })
    scene.setStations(stations({ a: 7, b: 1 }))
    const walkers = scene.spriteStates().filter((s) => s.mode === 'handoff')
    expect(walkers).toHaveLength(1)
    expect(walkers[0].stationKey).toBe('b')
    scene.settle()
    expect(crew(scene)).toEqual({ a: 6, b: 1 })
  })

  it('fades a walker out when its destination is full', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 2, b: 6 }))
    scene.setStations(stations({ a: 1, b: 7 }))
    for (let i = 0; i < 400; i++) scene.step(0.05)
    expect(crew(scene)).toEqual({ a: 1, b: 6 })
  })

  it('fades in spawns and fades out despawns', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 1, b: 1 }))
    scene.setStations(stations({ a: 2, b: 0 }))
    // a spawn (or a walker) appears; b's sprite is walking over or fading
    const modes = scene.spriteStates().map((s) => s.mode)
    expect(modes.some((m) => m === 'handoff' || m === 'fadeIn')).toBe(true)
    scene.setStations(stations({ a: 1, b: 0 }))
    expect(scene.spriteStates().some((s) => s.mode === 'fadeOut')).toBe(true)
    for (let i = 0; i < 400; i++) scene.step(0.05)
    expect(crew(scene)).toEqual({ a: 1 })
    expect(scene.spriteStates().every((s) => s.alpha === 1 && s.mode === 'local')).toBe(true)
  })

  it('settle() completes everything instantly', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 2, b: 1, c: 0 }))
    scene.setStations(stations({ a: 3, b: 0, c: 1 }))
    scene.settle()
    expect(scene.spriteStates().every((s) => s.mode === 'local' && s.alpha === 1)).toBe(true)
    expect(crew(scene)).toEqual({ a: 3, c: 1 })
  })

  it('does a full rebuild (no fades) when the station set changes', () => {
    const scene = new OfficeScene()
    scene.setStations(stations({ a: 2 }))
    scene.setStations(stations({ x: 1, y: 2 }))
    expect(scene.spriteStates().every((s) => s.mode === 'local')).toBe(true)
    expect(crew(scene)).toEqual({ x: 1, y: 2 })
  })

  it('keeps crews on their station when only the order changes', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 2, b: 1 }))
    scene.setStations(stations({ b: 1, a: 2 }))
    expect(crew(scene)).toEqual({ a: 2, b: 1 })
  })

  it('never lets gate crews take the work slot', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 4, g: 4 }, { g: { gate: true, action: 'waving' } }))
    for (let i = 0; i < 600; i++) {
      scene.step(0.05)
      expect(scene.spriteStates().some((s) => s.stationKey === 'g' && s.atWork)).toBe(false)
    }
  })

  it('lets non-gate crews work (control)', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.1)
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 3 }))
    let worked = false
    for (let i = 0; i < 600 && !worked; i++) {
      scene.step(0.05)
      worked = scene.spriteStates().some((s) => s.atWork)
    }
    expect(worked).toBe(true)
  })
})

describe('OfficeScene zoom', () => {
  it('scales the canvas size and reflows columns', () => {
    const scene = new OfficeScene()
    scene.setStations(stations({ a: 1, b: 1, c: 1, d: 1 }))
    scene.setZoom(3)
    scene.layout(1000)
    const w3 = scene.width()
    const h3 = scene.height()
    scene.setZoom(6)
    scene.layout(1000)
    expect(scene.width()).toBeLessThanOrEqual(1000)
    expect(scene.height()).toBeGreaterThan(h3) // fewer columns → more rows, bigger
    expect(w3).toBeLessThanOrEqual(1000)
  })
  it('auto zoom fits one row when possible', () => {
    const scene = new OfficeScene()
    scene.setStations(stations({ a: 1, b: 1, c: 1 }))
    scene.setZoom('auto')
    scene.layout(2000)
    expect(scene.width()).toBeLessThanOrEqual(2000)
    expect(scene.width()).toBeGreaterThan(1000) // zoom 6 → 1072
  })
})
