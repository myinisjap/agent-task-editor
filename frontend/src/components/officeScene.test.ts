import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { PARTICLE_CAP } from './officeParticles'
import {
  OfficeScene,
  autoZoom,
  resolveZoom,
  columnsFor,
  pairHandoffs,
  ease,
  tooClose,
  pickSeparatedSpot,
  queueRanks,
  queueFacing,
  QUEUE_OFFSETS,
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

// A Proxy 2D context: every method is a spy, with the few return values the
// scene reads back (gradients, image data, text metrics, transform).
function fakeCtx(calls: Record<string, number> = {}) {
  const target: Record<string, unknown> = {}
  return new Proxy(target, {
    get(_t, prop: string) {
      if (prop === 'getTransform') return () => ({ a: 1 })
      if (prop === 'measureText') return () => ({ width: 10 })
      if (prop === 'getImageData') return (_x: number, _y: number, w: number, h: number) => ({ data: new Uint8ClampedArray(w * h * 4) })
      if (prop in target) return target[prop]
      return (...args: unknown[]) => {
        calls[prop] = (calls[prop] ?? 0) + 1
        if (prop === 'createRadialGradient') return { addColorStop: vi.fn() }
        return args[0]
      }
    },
    set(t, prop: string, v) {
      t[prop] = v
      return true
    },
  }) as unknown as CanvasRenderingContext2D
}

describe('OfficeScene particles', () => {
  // Seeded randomness keeps these simulations deterministic.
  beforeEach(() => {
    let seed = 987
    vi.spyOn(Math, 'random').mockImplementation(() => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
      return seed / 4294967296
    })
  })
  it('emits sparks while hammering, stays under the cap, and settle() clears them', () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations([mk('w', 4, { action: 'hammering' })])
    let max = 0
    for (let i = 0; i < 1200; i++) {
      scene.step(1 / 60)
      max = Math.max(max, scene.particleCount())
    }
    expect(max).toBeGreaterThan(0)
    expect(max).toBeLessThanOrEqual(200)
    scene.settle()
    expect(scene.particleCount()).toBe(0)
  })

  it('stays under the cap with many celebrating crews', () => {
    const scene = new OfficeScene()
    scene.layout(2400)
    scene.setStations(Array.from({ length: 8 }, (_, i) => mk(`c${i}`, 6, { action: 'celebrating' })))
    let max = 0
    for (let i = 0; i < 600; i++) {
      scene.step(1 / 60)
      max = Math.max(max, scene.particleCount())
    }
    expect(max).toBeGreaterThan(0)
    expect(max).toBeLessThanOrEqual(200)
  })

  it('puffs steam and LEDs at the robot rack and walks robots without throwing', () => {
    const scene = new OfficeScene()
    scene.setRobots(true)
    scene.layout(1200)
    scene.setStations([mk('a', 2, { action: 'robot' }), mk('b', 0, { action: 'robot' })])
    scene.step(1 / 60)
    expect(scene.particleCount()).toBeGreaterThan(0)
    scene.setStations([mk('a', 1, { action: 'robot' }), mk('b', 1, { action: 'robot' })])
    expect(() => {
      for (let i = 0; i < 300; i++) scene.step(1 / 30)
    }).not.toThrow()
  })

  it('a moving robot-station sprite takes a side profile on horizontal legs', () => {
    // Seeded randomness keeps this deterministic.
    let seed = 12345
    vi.spyOn(Math, 'random').mockImplementation(() => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
      return seed / 4294967296
    })
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations([mk('a', 3, { action: 'robot' })])
    let side = false
    for (let i = 0; i < 6000 && !side; i++) {
      scene.step(1 / 30)
      side = scene.spriteStates().some((s) => s.moving && s.dir === 'side')
    }
    expect(side).toBe(true)
  })
})

describe('OfficeScene cached layers', () => {
  it('builds gradients once, reuses them across frames, and rebuilds only when dirtied', () => {
    const layerCalls: Record<string, number> = {}
    const scene = new OfficeScene()
    scene.canvasFactory = (w, h) => ({ width: w, height: h, getContext: () => fakeCtx(layerCalls) })
    scene.layout(1200)
    scene.setStations(stations({ a: 2, b: 1 }))
    const main = fakeCtx()
    const w = scene.width()
    const h = scene.height()
    expect(layerCalls.createRadialGradient ?? 0).toBe(0) // nothing built in layout/setStations
    scene.draw(main, w, h)
    const first = layerCalls.createRadialGradient
    expect(first).toBeGreaterThan(0)
    scene.step(1 / 60)
    scene.draw(main, w, h)
    scene.draw(main, w, h)
    expect(layerCalls.createRadialGradient).toBe(first)
    scene.setStations(stations({ a: 2, b: 1, c: 1 }))
    scene.draw(main, scene.width(), scene.height())
    expect(layerCalls.createRadialGradient).toBeGreaterThan(first)
  })

  it('draws without any canvas factory (direct fallback) and with reduced-motion settle', () => {
    const scene = new OfficeScene()
    scene.canvasFactory = () => null
    scene.layout(1200)
    scene.setStations(stations({ a: 2 }, { a: { gate: true, action: 'waving' } }))
    scene.settle()
    expect(() => scene.draw(fakeCtx(), scene.width(), scene.height())).not.toThrow()
    expect(scene.particleCount()).toBe(0)
    // Reduced motion: the only emote is the static gate "!".
    expect(scene.emoteStates()).toEqual([{ stationKey: "a", kind: 1 }])
  })
})

const seeded = () => {
  let seed = 4242
  vi.spyOn(Math, "random").mockImplementation(() => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
    return seed / 4294967296
  })
}

describe("separation helpers", () => {
  const script = (...v: number[]) => { let i = 0; return () => v[Math.min(i++, v.length - 1)] }
  it("tooClose is an ellipse", () => {
    expect(tooClose(0, 0)).toBe(true)
    expect(tooClose(29, 0)).toBe(true)
    expect(tooClose(31, 0)).toBe(false)
    expect(tooClose(0, 15)).toBe(false)
    expect(tooClose(22, 10)).toBe(false)
  })
  it("retries until a candidate is clear of the others", () => {
    const others = [{ tx: 0, ty: 0 }]
    // first candidate lands on the other, second is far away
    const r = pickSeparatedSpot(0, 100, 0, 100, others, 1, 5, script(0, 0, 0.9, 0.9))
    expect(r).toEqual({ x: 90, y: 90 })
  })
  it("takes the first candidate when there are no others", () => {
    expect(pickSeparatedSpot(10, 20, 10, 20, [], 0, 5, script(0.5, 0.5, 0.9, 0.9))).toEqual({ x: 15, y: 15 })
  })
  it("accepts the last candidate after the retries", () => {
    const others = [{ tx: 0, ty: 0 }]
    const r = pickSeparatedSpot(0, 10, 0, 10, others, 1, 3, script(0.1, 0.1, 0.2, 0.2, 0.3, 0.3, 0.9, 0.9))
    expect(r).toEqual({ x: 3, y: 3 })
  })
  it("collapses degenerate bounds to the minimum", () => {
    expect(pickSeparatedSpot(5, 5, 7, 2, [], 0)).toEqual({ x: 5, y: 7 })
  })
})

describe("queue helpers", () => {
  it("ranks by ticket with ties by index", () => {
    const out: number[] = []
    queueRanks([7, 3, 9], 3, out)
    expect(out).toEqual([1, 0, 2])
    queueRanks([4, 4, 1], 3, out)
    expect(out).toEqual([1, 2, 0])
  })
  it("has enough, distinct, in-bounds slots receding from the desk", () => {
    expect(QUEUE_OFFSETS.length).toBeGreaterThanOrEqual(5)
    let prevDy = -Infinity
    QUEUE_OFFSETS.forEach(([dx, dy], k) => {
      expect(Math.abs(dx)).toBeLessThanOrEqual(56)
      expect(dy).toBeLessThanOrEqual(66)
      expect(dy).toBeGreaterThan(prevDy) // the line recedes toward the viewer
      prevDy = dy
      for (let j = k + 1; j < QUEUE_OFFSETS.length; j++) {
        expect(Math.hypot(dx - QUEUE_OFFSETS[j][0], dy - QUEUE_OFFSETS[j][1])).toBeGreaterThanOrEqual(20)
      }
    })
  })
  it("faces the desk", () => {
    expect(queueFacing(100, 104)).toEqual({ dir: "back", facing: 1 })
    expect(queueFacing(60, 104)).toEqual({ dir: "side", facing: 1 })
    expect(queueFacing(150, 104)).toEqual({ dir: "side", facing: -1 })
  })
})

describe("OfficeScene crowd behaviour", () => {
  beforeEach(seeded)

  it("keeps one worker at the desk and a unique, gapless line", () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 5 }))
    let sawQueue = false
    for (let i = 0; i < 600; i++) {
      scene.step(0.05)
      const st = scene.spriteStates()
      expect(st.filter((s) => s.atWork).length).toBeLessThanOrEqual(1)
      const slots = st.filter((s) => s.queued).map((s) => s.qSlot).sort()
      if (slots.length) sawQueue = true
      // settled lines have unique slots (a fresh joiner may still be -1 for a moment)
      const assigned = slots.filter((q) => q >= 0)
      expect(new Set(assigned).size).toBe(assigned.length)
    }
    expect(sawQueue).toBe(true)
  })

  it("spreads parked sprites out instead of stacking", () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 6 }))
    for (let i = 0; i < 400; i++) scene.step(0.05)
    const st = scene.spriteStates().filter((s) => !s.moving && !s.atWork)
    for (let i = 0; i < st.length; i++)
      for (let j = i + 1; j < st.length; j++)
        expect(Math.hypot(st[i].x - st[j].x, st[i].y - st[j].y)).toBeGreaterThan(8)
  })

  it("shows at most one emote per station, and one \"!\" at a gate", () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ g: 4, p: 3 }, { g: { gate: true, action: "waving" }, p: { action: "drawing" } }))
    for (let i = 0; i < 400; i++) {
      scene.step(0.05)
      const e = scene.emoteStates()
      for (const key of ["g", "p"]) expect(e.filter((x) => x.stationKey === key).length).toBeLessThanOrEqual(1)
      expect(e.filter((x) => x.stationKey === "g" && x.kind !== 1)).toHaveLength(0)
      scene.draw(fakeCtx(), scene.width(), scene.height())
    }
  })

  it("pops a lightbulb after a planning turn", () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ p: 2 }, { p: { action: "drawing" } }))
    let bulb = false
    for (let i = 0; i < 1200 && !bulb; i++) {
      scene.step(0.05)
      bulb = scene.emoteStates().some((e) => e.kind === 3)
    }
    expect(bulb).toBe(true)
  })

  it("kicks up dust when walkers start and stays under the particle cap", () => {
    const scene = new OfficeScene()
    scene.layout(1200)
    scene.setStations(stations({ a: 6, b: 6, c: 6 }))
    let dust = false
    for (let i = 0; i < 300; i++) {
      scene.step(0.05)
      dust = dust || scene.particleCount() > 0
      expect(scene.particleCount()).toBeLessThanOrEqual(PARTICLE_CAP)
    }
    expect(dust).toBe(true)
  })

  it("shows a +N tag and draws robots with squash without throwing", () => {
    const scene = new OfficeScene()
    scene.setRobots(true)
    scene.layout(1200)
    scene.setStations(stations({ a: 9 }))
    for (let i = 0; i < 100; i++) {
      scene.step(0.05)
      scene.draw(fakeCtx(), scene.width(), scene.height())
    }
    expect(scene.particleCount()).toBe(0) // robots fly: no dust
  })
})
