// Imperative office-floor sim for the dashboard visualization. Holds sprite
// entities and station layout OUTSIDE React (like pixel-agents' OfficeState),
// stepped by a requestAnimationFrame loop in TaskFactory.tsx and drawn onto a
// Canvas 2D context with integer-zoom pixel blitting. No React, no DOM here.
//
// Layout is a RESPONSIVE GRID: TaskFactory measures its container and calls
// layout(availWidth); the scene wraps stations into as many columns as fit,
// so on a phone it stacks into 1-2 columns instead of a wide h-scroll row.
// Each station has ONE shared workstation (a desk/bench/anvil/…): sprites
// take turns walking up to a work slot beside it, act facing it, then step
// aside so the next worker can use it.
//
// ZOOM: the simulation runs in "base units" (CSS px at the default zoom 3, so
// one art pixel = ART_PX = 3 units). Zoom is only a render scale
// (scale = zoom / 3): positions/tweens never depend on it, so a zoom change
// cannot strand sprites, and one art pixel is always `zoom` whole CSS px.
//
// HAND-OFFS: setStations() diffs instead of rebuilding. A station whose count
// drops paired with one whose count rises sends a sprite walking between them
// (mode 'handoff'); unpaired changes fade sprites in/out.
import {
  composeFrame,
  actionFrameCount,
  actionFrameDur,
  actionBob,
  VARIANTS,
  WALK_FRAME_COUNT,
  WALK_BOB,
  ROBOT_HOVER_BOB,
  isImpactFrame,
  SHADOW_ROWS,
  SHADOW_ALPHA,
  GRID_W,
  GRID_H,
  type Action,
  type Dir,
  type Frame,
  type Variant,
} from './pixelSprites'
import {
  ParticlePool,
  PARTICLE_CAP,
  PARTICLE_COLORS,
  SPARK,
  PAPER,
  STEAM,
  LED,
  CONFETTI,
  DUST,
} from './officeParticles'
import { pickClip, propFor, PROP_RECTS, WORKER_SHEET, ROBOT_SHEET, type PropName, type Sheets } from './officeSheets'

export type Station = {
  key: string
  name: string
  color: string
  action: Action
  count: number
  gate?: boolean // human gate: the crew waves for attention instead of working
}

export type SpriteMode = 'local' | 'handoff' | 'fadeIn' | 'fadeOut'

// One character on the floor. Positions are in base units (top-left of the
// sprite's grid box). A sprite is either walking (an eased tween from→to) or
// parked (acting). If it holds the work slot it acts facing the workstation,
// then yields the slot and wanders to an idle spot before queueing again.
type Sprite = {
  stationIndex: number
  stationKey: string
  mode: SpriteMode
  alpha: number
  overflow: boolean // handoff walker bound for a full station: fades on arrival
  x: number
  y: number
  fromX: number
  fromY: number
  tx: number
  ty: number
  t: number // tween clock (s)
  dur: number // tween length (s)
  speed: number // units/sec (sizes the tween duration)
  facing: 1 | -1
  moving: boolean
  atWork: boolean // currently occupying this station's work slot
  pause: number // seconds left parked before next move
  frameTimer: number
  jitter: number // per-sprite action tempo multiplier
  frame: number
  legDist: number // distance walked since the last leg step
  leg: number // walk-cycle index (0..WALK_FRAME_COUNT-1)
  dir: Dir // facing: front (toward viewer) / side / back (away/at station)
  variant: Variant
  blinkTimer: number
  blinkLeft: number
  breathPhase: number
  startT: number // walk-start anticipation left (s)
  settleT: number // feet-together settle left after arriving (s)
  prevFrame: number // last action frame seen (particle emission on change)
  tick: number // monotonic action-frame counter (PNG sheet clips of any length)
  steps: number // monotonic walk-step counter (PNG sheet walk cycles)
  burstCool: number // confetti rate limit (s)
  queued: boolean // waiting in line for the station's work slot
  ticket: number // order of joining the line
  qSlot: number // line position (0 = next up), -1 when not queued
  waitT: number // seconds spent queued
  fidget: 0 | 1 | 2 | 3 // none, turn, wave, look
  fidgetAge: number
  fidgetDur: number
  fidgetCool: number
  fidgetSide: 1 | -1
  squashT: number // robot landing squash left (s)
  // Last composed frame + the inputs it was built from (skip recomposing).
  lastFrame: Frame | null
  fAct: Action
  fIdx: number
  fLeg: number
  fDir: Dir
  fFlags: number
}

// Zoom. ART_PX is base units per art pixel; DEFAULT_ZOOM is CSS px per art
// pixel at scale 1. MIN_AUTO_ZOOM is the floor for 'auto' (so phones/laptops
// never get smaller sprites than the default); MAX_ZOOM caps everything.
export const DEFAULT_ZOOM = 3
export const MIN_AUTO_ZOOM = 3
export const MAX_ZOOM = 6
const ART_PX = 3
const SPRITE_W = GRID_W * ART_PX // 48
const SPRITE_H = GRID_H * ART_PX // 72
const CAP = 6 // max sprites drawn per station
const STATION_W = 168
const STATION_GAP = 0 // no gap: adjacent stations abut into one continuous scene
const PAD = 16
// Stations sit flush on one shared floor; the label + count live on the
// workstation sign, so there's no header/footer text band around each cell.
const SIGN_H = 30 // room above the desk reserved for the name/count plaque
const FLOOR_H = 190 // patch height sprites roam within
const CELL_H = SIGN_H + FLOOR_H // full vertical footprint of one station

const STRIDE = 4 // base units walked per walk-cycle frame
const FADE_S = 0.35
const HANDOFF_SPEED = 70
const BREATH_PERIOD = 2.4
const DUST_COLOR = 16 // first dust palette index
const EMOTE_POP = 0.07 // half-size pop-in (s)
const EMOTE_FADE = 0.3
const SQUASH_S = 0.08
const SEP_SPEED = 14 // units/s nudge apart

export const SEP_X = 30
export const SEP_Y = 14

type Patch = { x: number; y: number; w: number; h: number }

// ---- pure helpers (unit-tested) -------------------------------------------

/** easeInOutSine on [0,1]. */
export function ease(u: number): number {
  const c = Math.max(0, Math.min(1, u))
  return 0.5 - Math.cos(Math.PI * c) / 2
}

/** Largest integer zoom in [MIN_AUTO_ZOOM, MAX_ZOOM] whose single-row layout fits. */
export function autoZoom(availWidth: number, stationCount: number): number {
  const n = Math.max(1, stationCount)
  const baseW = PAD * 2 + n * STATION_W
  for (let z = MAX_ZOOM; z > MIN_AUTO_ZOOM; z--) {
    if ((baseW * z) / DEFAULT_ZOOM <= availWidth) return z
  }
  return MIN_AUTO_ZOOM
}

/** Resolve the zoom option: 'auto', or a number rounded and clamped to [1, MAX_ZOOM]. */
export function resolveZoom(zoom: number | 'auto', availWidth: number, stationCount: number): number {
  if (zoom === 'auto') return autoZoom(availWidth, stationCount)
  if (!Number.isFinite(zoom)) return DEFAULT_ZOOM
  return Math.max(1, Math.min(MAX_ZOOM, Math.round(zoom)))
}

/** Grid columns that fit `availWidth` CSS px at `zoom` (always in [1, n]). */
export function columnsFor(availWidth: number, zoom: number, stationCount: number): number {
  const baseAvail = availWidth / (zoom / DEFAULT_ZOOM)
  const usable = Math.max(STATION_W, baseAvail - PAD * 2)
  const fit = Math.floor((usable + STATION_GAP) / (STATION_W + STATION_GAP))
  return Math.max(1, Math.min(stationCount || 1, fit))
}

export type Handoffs = { moves: { from: string; to: string }[]; spawns: string[]; despawns: string[] }

/**
 * Pair count decrements with increments between two station snapshots. Each
 * unit of change is one entry; a decrement walks to the first remaining
 * increment DOWNSTREAM of it (higher station index — tasks mostly move
 * forward), else to the first remaining increment. Unpaired increments are
 * spawns, unpaired decrements despawns. Deterministic and pure; uses true
 * counts (not the capped visible ones).
 */
export function pairHandoffs(prev: Station[], next: Station[]): Handoffs {
  const before = new Map(prev.map((s) => [s.key, s.count]))
  const dec: { key: string; i: number }[] = []
  let inc: { key: string; i: number }[] = []
  next.forEach((s, i) => {
    const d = s.count - (before.get(s.key) ?? 0)
    for (let k = 0; k < Math.abs(d); k++) (d < 0 ? dec : inc).push({ key: s.key, i })
  })
  const moves: Handoffs['moves'] = []
  for (const d of dec) {
    if (!inc.length) break
    let j = inc.findIndex((u) => u.i > d.i)
    if (j < 0) j = 0
    moves.push({ from: d.key, to: inc[j].key })
    inc = inc.filter((_, idx) => idx !== j)
  }
  return {
    moves,
    spawns: inc.map((u) => u.key),
    despawns: dec.slice(moves.length).map((u) => u.key),
  }
}

/** True when two sprites at offset (dx, dy) overlap: an ellipse (sprites are wide, depth spacing is small). */
export function tooClose(dx: number, dy: number): boolean {
  const ex = dx / SEP_X
  const ey = dy / SEP_Y
  return ex * ex + ey * ey < 1
}

/**
 * Pick a spot in [minX,maxX]×[minY,maxY] that isn't too close to any of the
 * first `count` entries of `others` (by tx/ty). Up to `tries` samples, then
 * the last one is accepted. Degenerate bounds collapse to the minimum.
 */
export function pickSeparatedSpot(
  minX: number, maxX: number, minY: number, maxY: number,
  others: ArrayLike<{ tx: number; ty: number }>, count: number,
  tries = 5, rnd: () => number = Math.random,
): { x: number; y: number } {
  let x = minX
  let y = minY
  for (let t = 0; t < tries; t++) {
    x = maxX > minX ? minX + rnd() * (maxX - minX) : minX
    y = maxY > minY ? minY + rnd() * (maxY - minY) : minY
    let ok = true
    for (let k = 0; k < count; k++) {
      if (tooClose(x - others[k].tx, y - others[k].ty)) { ok = false; break }
    }
    if (ok) break
  }
  return { x, y }
}

// Waiting line, as offsets from the work slot's top-left: a diagonal receding
// down-left from the desk's right front corner. Nearest first.
export const QUEUE_OFFSETS: readonly (readonly [dx: number, dy: number])[] = [
  [46, 6], [28, 26], [6, 44], [-18, 58], [-44, 64],
]

/** Pose of a sprite standing in the line at slotX, looking at the work slot at workX. */
export function queueFacing(slotX: number, workX: number): { dir: Dir; facing: 1 | -1 } {
  if (Math.abs(slotX - workX) < 12) return { dir: 'back', facing: 1 }
  return { dir: 'side', facing: workX >= slotX ? 1 : -1 }
}

/** Line rank (0 = first) of each of the first n tickets: ascending ticket, ties by index. */
export function queueRanks(tickets: ArrayLike<number>, n: number, out: Int8Array | number[]): void {
  for (let i = 0; i < n; i++) {
    let r = 0
    for (let j = 0; j < n; j++) if (tickets[j] < tickets[i] || (tickets[j] === tickets[i] && j < i)) r++
    out[i] = r
  }
}

// Emote glyphs: row bitmasks (MSB = leftmost column). 3 wide, except the check (5 wide).
const GLYPHS: readonly (readonly number[])[] = [
  [],
  [2, 2, 2, 0, 2], // !
  [7, 1, 3, 0, 2], // ?
  [7, 5, 7, 2, 2], // lightbulb (bottom two rows are the base)
  [1, 2, 20, 8], // check
]

const rand = (a: number, b: number) => a + Math.random() * (b - a)
const actionOf = (st: Station): Action => (st.gate ? 'waving' : st.action)

export class OfficeScene {
  stations: Station[] = []
  robots = false // render the crew as robots (all-robot version)
  private sprites: Sprite[] = []
  private aisle = 0
  private clock = 0
  private cols = 1
  private layoutW = STATION_W + PAD * 2
  private zoomOpt: number | 'auto' = DEFAULT_ZOOM
  private zoom = DEFAULT_ZOOM
  private scale = 1
  private availW = STATION_W + PAD * 2
  private ordered: Sprite[] = []
  private particles = new ParticlePool(PARTICLE_CAP)
  private stationEmit = new Float32Array(0) // per station: [steam timer, LED timer]
  private tmpX = 0
  private tmpY = 0
  private ticketSeq = 0
  private near: Sprite[] = [] // scratch: obstacles when picking a spot
  private qScratch: Sprite[] = [] // scratch: one station's line
  private sepScratch: Sprite[] = [] // scratch: parked sprites of one station
  private overflowLabel: string[] = [] // "+N" per station, built in setStations
  private bMinX = 0
  private bMaxX = 0
  private bMinY = 0
  private bMaxY = 0
  // One emote bubble per station at most.
  private emoteKind = new Uint8Array(0) // 0 none, 1 !, 2 ?, 3 bulb, 4 check
  private emoteAge = new Float32Array(0)
  private emoteLife = new Float32Array(0) // for "!": seconds until it moves to another crew member
  private emoteOwner: (Sprite | null)[] = []
  // Cached layers (built lazily in draw, dirtied by layout/stations/zoom).
  canvasFactory: (w: number, h: number) => CanvasLike | null = defaultCanvas
  private lastLayer: CanvasLike | null = null
  private bg: CanvasLike | null = null
  private front: CanvasLike | null = null
  private shade: CanvasLike | null = null
  private glow: CanvasLike | null = null
  private bgDirty = true
  private lightDirty = true
  private bgW = -1
  private bgH = -1
  private lightW = 0
  private lightH = 0

  // PNG sprite sheets (officeSheets.ts). Null until they load: the grid
  // sprites from pixelSprites.ts are drawn meanwhile and in tests.
  private sheets: Sheets | null = null
  setSheets(sheets: Sheets | null) {
    this.sheets = sheets
    this.bgDirty = true // furniture props live in the cached background
  }
  usingSheets(): boolean {
    return this.sheets !== null
  }

  setRobots(on: boolean) {
    this.robots = on
  }

  // Zoom option: integer (rounded, clamped to [1, 6]) or 'auto'. Only changes
  // the render scale; TaskFactory re-runs layout()/resize afterwards.
  setZoom(z: number | 'auto') {
    this.zoomOpt = z
    this.applyZoom()
    this.bgDirty = true
  }

  private applyZoom() {
    this.zoom = resolveZoom(this.zoomOpt, this.availW, this.stations.length)
    this.scale = this.zoom / DEFAULT_ZOOM
  }

  setStations(next: Station[]) {
    const prev = this.stations
    const sameKeys =
      prev.length > 0 && prev.length === next.length && next.every((s) => prev.some((p) => p.key === s.key))
    this.stations = next
    this.applyZoom()
    this.relayout()
    this.bgDirty = true
    this.lightDirty = true
    if (this.stationEmit.length !== next.length * 2) this.stationEmit = new Float32Array(next.length * 2)
    if (this.emoteKind.length !== next.length) {
      this.emoteKind = new Uint8Array(next.length)
      this.emoteAge = new Float32Array(next.length)
      this.emoteLife = new Float32Array(next.length)
      this.emoteOwner = new Array<Sprite | null>(next.length).fill(null)
    }
    this.overflowLabel = next.map((s) => (s.count > CAP ? `+${s.count - CAP}` : ''))
    if (!sameKeys) {
      this.rebuildSprites() // first call or workflow switch: no fades
      return
    }
    const orderChanged = next.some((s, i) => prev[i].key !== s.key)
    const index = new Map(next.map((s, i) => [s.key, i]))
    for (const s of this.sprites) s.stationIndex = index.get(s.stationKey) ?? s.stationIndex
    if (orderChanged) this.replaceSprites()

    const { moves, spawns, despawns } = pairHandoffs(prev, next)
    for (const m of moves) this.applyMove(m.from, m.to)
    for (const key of spawns) if (this.live(key) < this.want(key)) this.spawnFade(key)
    for (const key of despawns) if (this.live(key) > this.want(key)) this.fadeOutOne(key)
    this.reconcile()
  }

  // ---- hand-off bookkeeping ------------------------------------------------

  private want(key: string): number {
    const st = this.stations.find((s) => s.key === key)
    return st ? Math.min(st.count, CAP) : 0
  }

  // Sprites that count toward a station's visible crew (walkers count toward
  // their destination; overflow walkers and fading-out sprites don't).
  private live(key: string): number {
    let n = 0
    for (const s of this.sprites) if (s.stationKey === key && s.mode !== 'fadeOut' && !s.overflow) n++
    return n
  }

  private pickLocal(key: string): Sprite | undefined {
    const c = this.sprites.filter((s) => s.stationKey === key && (s.mode === 'local' || s.mode === 'fadeIn'))
    const free = c.filter((s) => !s.atWork)
    const pool = free.length ? free : c
    return pool[Math.floor(Math.random() * pool.length)]
  }

  private applyMove(fromKey: string, toKey: string) {
    const di = this.stations.findIndex((s) => s.key === toKey)
    const si = this.stations.findIndex((s) => s.key === fromKey)
    if (di < 0 || si < 0) return
    let w: Sprite | undefined
    if (this.live(fromKey) > this.want(fromKey)) w = this.pickLocal(fromKey)
    if (!w) {
      // Source is still at CAP (overflow): the walker leaves from its work slot.
      const slot = this.workSlot(si)
      w = this.makeSprite(si, slot.x, slot.y)
      this.sprites.push(w)
    }
    w.overflow = this.live(toKey) >= this.want(toKey)
    w.mode = 'handoff'
    w.alpha = 1
    w.atWork = false
    w.queued = false
    w.qSlot = -1
    w.stationIndex = di
    w.stationKey = toKey
    w.speed = HANDOFF_SPEED + Math.random() * 10
    const spot = this.idleTarget(di, w)
    this.startMove(w, spot.x, spot.y, 0.8, 4)
  }

  private spawnFade(key: string) {
    const i = this.stations.findIndex((s) => s.key === key)
    if (i < 0) return
    const spot = this.idleTarget(i)
    const s = this.makeSprite(i, spot.x, spot.y)
    s.mode = 'fadeIn'
    s.alpha = 0
    this.sprites.push(s)
  }

  private fadeOutOne(key: string) {
    const v = this.pickLocal(key) ?? this.sprites.find((s) => s.stationKey === key && s.mode === 'handoff' && !s.overflow)
    if (!v) return
    v.mode = 'fadeOut'
    v.atWork = false
    v.queued = false
    v.qSlot = -1
    v.moving = false
  }

  // Enforce min(count, CAP) visible per station against whatever drifted.
  private reconcile() {
    for (const st of this.stations) {
      while (this.live(st.key) < this.want(st.key)) this.spawnFade(st.key)
      while (this.live(st.key) > this.want(st.key)) {
        const before = this.live(st.key)
        this.fadeOutOne(st.key)
        if (this.live(st.key) === before) break
      }
    }
  }

  // Choose column count from available width, then remember the resulting
  // canvas size. Called on station change and on container resize. When the
  // column count changes, station patches move to new grid cells, so any
  // existing sprites must be re-snapped into their (new) patch or they'd be
  // left stranded outside it (invisible).
  layout(availWidth: number) {
    this.availW = availWidth
    this.applyZoom()
    const cols = columnsFor(availWidth, this.zoom, this.stations.length)
    const changed = cols !== this.cols
    this.cols = cols
    this.relayout()
    this.bgDirty = true
    this.lightDirty = true
    if (changed) this.replaceSprites()
  }

  // Drop every sprite onto a fresh idle spot inside its current patch. Used
  // after a column-count change so nobody is left in a stale grid cell.
  // Hand-off walkers keep walking, just toward a spot in the new patch.
  private replaceSprites() {
    this.particles.clear()
    for (const s of this.sprites) {
      const spot = this.idleTarget(s.stationIndex, s)
      s.queued = false
      s.qSlot = -1
      if (s.mode === 'handoff') {
        this.startMove(s, spot.x, spot.y, 0.8, 4)
        continue
      }
      s.x = spot.x
      s.y = spot.y
      s.fromX = spot.x
      s.fromY = spot.y
      s.tx = spot.x
      s.ty = spot.y
      s.moving = false
      s.atWork = false
      s.pause = Math.random() * 1.5
    }
  }

  private relayout() {
    const cols = Math.max(1, this.cols)
    this.layoutW = PAD * 2 + cols * STATION_W + (cols - 1) * STATION_GAP
  }

  /** CSS px width of the canvas (base layout × zoom scale). */
  width(): number {
    return Math.ceil(this.layoutW * this.scale)
  }

  height(): number {
    const rows = Math.max(1, Math.ceil((this.stations.length || 1) / Math.max(1, this.cols)))
    return Math.ceil((PAD * 2 + rows * CELL_H) * this.scale)
  }

  private patch(i: number): Patch {
    const col = i % this.cols
    const row = Math.floor(i / this.cols)
    return {
      x: PAD + col * STATION_W,
      y: PAD + row * CELL_H + SIGN_H,
      w: STATION_W,
      h: FLOOR_H,
    }
  }

  // The workstation footprint: a block centered along the patch's back wall
  // (upper area). Sprites work at a slot just below its front edge. The name/
  // count sign sits in the SIGN_H band directly above it.
  private workstation(i: number): { x: number; y: number; w: number; h: number } {
    const p = this.patch(i)
    const w = 84
    const h = 46
    return { x: p.x + p.w / 2 - w / 2, y: p.y + 12, w, h }
  }

  // The single work slot (top-left of a sprite standing at the station,
  // facing up into it).
  private workSlot(i: number): { x: number; y: number } {
    const ws = this.workstation(i)
    return { x: ws.x + ws.w / 2 - SPRITE_W / 2, y: ws.y + ws.h - 6 }
  }

  // Same as workSlot() but writes tmpX/tmpY (no allocation).
  private workSlotXY(i: number) {
    this.wsBox(i)
    this.tmpX += 42 - SPRITE_W / 2
    this.tmpY += 46 - 6
  }

  // Slot k of station i's waiting line (writes tmpX/tmpY).
  private queueSlotPos(k: number, i: number) {
    this.workSlotXY(i)
    const o = QUEUE_OFFSETS[k < 0 ? 0 : k >= QUEUE_OFFSETS.length ? QUEUE_OFFSETS.length - 1 : k]
    this.tmpX += o[0]
    this.tmpY += o[1]
  }

  // Idle band of station i, in bMinX..bMaxY (no allocation).
  private idleBounds(i: number) {
    const col = i % this.cols
    const row = Math.floor(i / this.cols)
    const px = PAD + col * STATION_W
    const py = PAD + row * CELL_H + SIGN_H
    this.bMinX = px + 4
    this.bMaxX = px + STATION_W - SPRITE_W - 4
    // Lower ~half of the patch, below the workstation, but a generous band so
    // a crew spreads out instead of stacking on one line.
    this.bMinY = py + FLOOR_H * 0.42
    this.bMaxY = py + FLOOR_H - SPRITE_H
  }

  // A random idle spot in the lower part of the patch (out of the way of the
  // workstation), where sprites wait between turns. Avoids the current
  // targets of the station's other sprites (queue slots included).
  private idleTarget(i: number, self?: Sprite): { x: number; y: number } {
    this.idleBounds(i)
    const near = this.near
    near.length = 0
    for (let k = 0; k < this.sprites.length; k++) {
      const o = this.sprites[k]
      if (o !== self && o.stationIndex === i && o.mode !== 'fadeOut' && !o.overflow) near.push(o)
    }
    const r = pickSeparatedSpot(this.bMinX, this.bMaxX, this.bMinY, this.bMaxY, near, near.length)
    near.length = 0
    return r
  }

  private makeSprite(i: number, x: number, y: number): Sprite {
    const st = this.stations[i]
    return {
      stationIndex: i,
      stationKey: st.key,
      mode: 'local',
      alpha: 1,
      overflow: false,
      x, y, fromX: x, fromY: y, tx: x, ty: y,
      t: 0,
      dur: 1,
      speed: 16 + Math.random() * 14,
      facing: 1,
      moving: false,
      atWork: false,
      pause: Math.random() * 1.5,
      frameTimer: Math.random(),
      jitter: rand(0.8, 1.25),
      frame: Math.floor(Math.random() * 8),
      legDist: 0,
      leg: 0,
      dir: 'front',
      variant: VARIANTS[Math.floor(Math.random() * VARIANTS.length)],
      blinkTimer: rand(2, 5),
      blinkLeft: 0,
      breathPhase: Math.random() * BREATH_PERIOD,
      startT: 0,
      settleT: 0,
      prevFrame: -1,
      tick: Math.floor(Math.random() * 8),
      steps: 0,
      burstCool: 0,
      queued: false,
      ticket: 0,
      qSlot: -1,
      waitT: 0,
      fidget: 0,
      fidgetAge: 0,
      fidgetDur: 0,
      fidgetCool: rand(2, 7),
      fidgetSide: 1,
      squashT: 0,
      lastFrame: null,
      fAct: 'idle',
      fIdx: -1,
      fLeg: -2,
      fDir: 'front',
      fFlags: -1,
    }
  }

  private rebuildSprites() {
    this.sprites = []
    this.particles.clear()
    this.clearEmotes()
    this.stations.forEach((st, i) => {
      const n = Math.min(st.count, CAP)
      for (let k = 0; k < n; k++) {
        const start = this.idleTarget(i)
        const s = this.makeSprite(i, start.x, start.y)
        this.sprites.push(s)
        const t = this.idleTarget(i, s)
        this.startMove(s, t.x, t.y)
      }
    })
  }

  // Begin an eased move toward (tx,ty). Duration = distance / speed, clamped.
  // Facing comes from the whole leg of travel (to - from), not per-frame dx.
  private startMove(s: Sprite, tx: number, ty: number, minDur = 0.4, maxDur = Infinity) {
    s.fromX = s.x
    s.fromY = s.y
    s.tx = tx
    s.ty = ty
    const dx = tx - s.x
    const dy = ty - s.y
    const dist = Math.hypot(dx, dy)
    s.t = 0
    s.dur = Math.max(minDur, Math.min(maxDur, dist / s.speed))
    s.moving = true
    s.legDist = 0
    s.startT = 0.1 // brief anticipation before the first step
    s.settleT = 0
    s.fidget = 0
    // Dominant axis: mostly-horizontal → side profile (mirrored via facing);
    // mostly-vertical → back if walking up, front if walking down.
    if (Math.abs(dx) >= Math.abs(dy)) {
      s.dir = 'side'
      if (Math.abs(dx) > 0.5) s.facing = dx < 0 ? -1 : 1
    } else {
      s.dir = dy < 0 ? 'back' : 'front'
    }
  }

  // Tween finished (or settled): park, and for hand-off walkers join the crew.
  private arrive(s: Sprite) {
    s.x = s.tx
    s.y = s.ty
    s.moving = false
    if (s.mode === 'handoff') {
      const key = s.stationKey
      s.speed = 16 + Math.random() * 14
      s.atWork = false
      if (s.overflow && this.live(key) >= this.want(key)) {
        s.mode = 'fadeOut' // destination is full: join the overflow
      } else {
        s.overflow = false
        s.mode = 'local'
      }
    }
    const st = this.stations[s.stationIndex]
    s.fidget = 0
    // At the work slot: face the station (back to viewer) and stay a while to
    // act; otherwise idle facing the viewer (front). Gate crews linger waving.
    s.settleT = 0.12
    const wasSide = s.dir === 'side'
    s.pause = s.atWork ? 2.5 + Math.random() * 3 : st?.gate ? 2.5 + Math.random() * 3 : 0.6 + Math.random() * 1.8
    // Sometimes park in profile after a sideways leg so the side action frames show.
    if (s.queued) {
      // Standing in line: look toward the desk.
      this.workSlotXY(s.stationIndex)
      const qf = queueFacing(s.tx, this.tmpX)
      s.dir = qf.dir
      s.facing = qf.facing
    } else {
      s.dir = s.atWork ? 'back' : !st?.gate && wasSide && Math.random() < 0.4 ? 'side' : 'front'
      if (s.atWork) s.facing = 1
    }
    // Robots land with a tiny squash.
    if (this.robots || (st && actionOf(st) === 'robot')) s.squashT = SQUASH_S
  }

  // Small dust puff at a walker's feet (workers only; robots fly).
  private puff(s: Sprite) {
    const st = this.stations[s.stationIndex]
    if (this.robots || !st || actionOf(st) === 'robot' || s.alpha < 1) return
    const n = 2 + Math.floor(Math.random() * 3)
    for (let k = 0; k < n; k++) {
      this.particles.spawn(
        DUST, s.x + SPRITE_W / 2 + rand(-6, 6), s.y + SPRITE_H - 2,
        rand(-28, 28), rand(-14, -4), 30, rand(0.22, 0.38), DUST_COLOR + Math.floor(Math.random() * 3),
      )
    }
  }

  // The action a sprite plays when parked: gate crews wave, queued workers
  // breathe idly instead of air-hammering, everyone else does the station job.
  private poseAction(s: Sprite, st: Station): Action {
    return st.gate ? 'waving' : s.queued && st.action !== 'robot' ? 'idle' : st.action
  }

  // Effective pose (fidgets applied) into pAct/pDir/pFacing/pIdx. s.dir is
  // never changed by a fidget. pIdx < 0 means "use the sprite's own frame".
  private pAct: Action = 'idle'
  private pDir: Dir = 'front'
  private pFacing: 1 | -1 = 1
  private pIdx = -1
  private resolvePose(s: Sprite, st: Station) {
    let act = this.poseAction(s, st)
    let dir = s.dir
    let facing = s.facing
    let idx = -1
    if (s.fidget && !s.moving && !s.atWork && s.settleT <= 0 && !st.gate) {
      const robotStation = st.action === 'robot'
      if (s.fidget === 1) {
        if (dir === 'side') dir = 'front'
        else { dir = 'side'; facing = s.fidgetSide }
      } else if (s.fidget === 2) {
        if (!robotStation) act = 'waving'
        dir = 'front'
        idx = Math.floor(s.fidgetAge / (actionFrameDur('waving') * s.jitter))
      } else {
        if (!robotStation) act = 'idle'
        const ph = Math.min(3, Math.floor(s.fidgetAge / 0.3))
        if (ph === 1) dir = 'back'
        else if (ph === 3) dir = 'front'
        else { dir = 'side'; facing = ph === 0 ? s.fidgetSide : ((-s.fidgetSide) as 1 | -1) }
      }
    }
    this.pAct = act
    this.pDir = dir
    this.pFacing = facing
    this.pIdx = idx
  }

  private stepFidget(s: Sprite, dt: number) {
    if (s.fidget) {
      s.fidgetAge += dt
      if (s.fidgetAge >= s.fidgetDur) { s.fidget = 0; s.fidgetCool = rand(3, 7) }
      return
    }
    s.fidgetCool -= dt
    if (s.fidgetCool > 0) return
    const k = 1 + Math.floor(Math.random() * 3)
    s.fidget = k as 1 | 2 | 3
    s.fidgetAge = 0
    s.fidgetSide = Math.random() < 0.5 ? 1 : -1
    s.fidgetDur = k === 1 ? 0.7 : k === 2 ? 4 * actionFrameDur('waving') * s.jitter : 1.2
    s.fidgetCool = rand(3, 7)
  }

  // True if any sprite currently owns station i's work slot.
  private slotTaken(stationIndex: number): boolean {
    return this.sprites.some((s) => s.stationIndex === stationIndex && s.atWork)
  }

  private anyQueued(stationIndex: number): boolean {
    for (let k = 0; k < this.sprites.length; k++) {
      const s = this.sprites[k]
      if (s.stationIndex === stationIndex && s.queued) return true
    }
    return false
  }

  /** Test introspection only (read-only): the current sprite roster. */
  spriteStates(): {
    stationKey: string; mode: SpriteMode; alpha: number; atWork: boolean; dir: Dir; moving: boolean
    queued: boolean; qSlot: number; x: number; y: number
  }[] {
    return this.sprites.map((s) => ({
      stationKey: s.stationKey, mode: s.mode, alpha: s.alpha, atWork: s.atWork, dir: s.dir, moving: s.moving,
      queued: s.queued, qSlot: s.qSlot, x: s.x, y: s.y,
    }))
  }

  /** Test introspection only: the visible emote (1 "!", 2 "?", 3 bulb, 4 check) per station. */
  emoteStates(): { stationKey: string; kind: number }[] {
    const out: { stationKey: string; kind: number }[] = []
    for (let i = 0; i < this.stations.length; i++) {
      if (this.emoteKind[i] && this.emoteOwner[i]) out.push({ stationKey: this.stations[i].key, kind: this.emoteKind[i] })
    }
    return out
  }

  private clearEmotes() {
    this.emoteKind.fill(0)
    for (let i = 0; i < this.emoteOwner.length; i++) this.emoteOwner[i] = null
  }

  private setEmote(i: number, kind: number, owner: Sprite, life: number) {
    if (i < 0 || i >= this.emoteKind.length) return
    this.emoteKind[i] = kind
    this.emoteAge[i] = 0
    this.emoteLife[i] = life
    this.emoteOwner[i] = owner
  }

  // A random parked local sprite of station i (optionally only long-waiting queuers).
  private pickEmoteOwner(i: number, waiting: boolean): Sprite | null {
    let n = 0
    for (let k = 0; k < this.sprites.length; k++) if (this.emoteCandidate(this.sprites[k], i, waiting)) n++
    if (n === 0) return null
    let r = Math.floor(Math.random() * n)
    for (let k = 0; k < this.sprites.length; k++) {
      const c = this.sprites[k]
      if (this.emoteCandidate(c, i, waiting) && r-- === 0) return c
    }
    return null
  }

  private emoteCandidate(s: Sprite, i: number, waiting: boolean): boolean {
    if (s.stationIndex !== i || s.mode !== 'local' || s.moving || s.alpha < 1) return false
    return waiting ? s.queued && (s.waitT > 6 || s.qSlot >= 2) : true
  }

  private stepEmotes(dt: number) {
    for (let i = 0; i < this.stations.length && i < this.emoteKind.length; i++) {
      const st = this.stations[i]
      const k = this.emoteKind[i]
      if (k) {
        const o = this.emoteOwner[i]
        this.emoteAge[i] += dt
        let drop = !o || o.mode === 'fadeOut' || o.mode === 'handoff' || o.stationIndex !== i
        if (k === 1) {
          // "!" lives while the station is a gate, hopping between crew members.
          if (!st.gate || (o && o.moving) || this.emoteAge[i] >= this.emoteLife[i]) drop = true
        } else if (this.emoteAge[i] >= this.emoteLife[i]) drop = true
        if (drop) { this.emoteKind[i] = 0; this.emoteOwner[i] = null }
      }
      if (this.emoteKind[i] !== 0) continue
      if (st.gate) {
        const o = this.pickEmoteOwner(i, false)
        if (o) this.setEmote(i, 1, o, rand(2.5, 4))
      } else if (Math.random() < dt * 0.12) {
        const o = this.pickEmoteOwner(i, true)
        if (o) this.setEmote(i, 2, o, 1.6)
      }
    }
  }

  // Walkers in line: keep the line ordered by ticket, promote the head when
  // the desk is free, and shuffle everyone forward with the normal walk.
  private updateQueues(dt: number) {
    const q = this.qScratch
    for (let i = 0; i < this.stations.length; i++) {
      q.length = 0
      for (let k = 0; k < this.sprites.length; k++) {
        const s = this.sprites[k]
        if (s.stationIndex !== i || !s.queued || (s.mode !== 'local' && s.mode !== 'fadeIn')) continue
        let j = q.length
        q.push(s)
        while (j > 0 && q[j - 1].ticket > s.ticket) { q[j] = q[j - 1]; j-- }
        q[j] = s
      }
      if (q.length === 0) continue
      let first = 0
      if (!this.slotTaken(i)) {
        const h = q[0]
        h.queued = false
        h.qSlot = -1
        h.atWork = true
        this.workSlotXY(i)
        this.startMove(h, this.tmpX, this.tmpY)
        first = 1
      }
      for (let k = first; k < q.length; k++) {
        const s = q[k]
        const rank = k - first
        s.waitT += dt
        if (s.qSlot !== rank) {
          s.qSlot = rank
          this.queueSlotPos(rank, i)
          this.startMove(s, this.tmpX, this.tmpY)
        }
      }
    }
    q.length = 0
  }

  // Gentle push-apart for parked sprites that overlap. Queued and working
  // sprites are fixed obstacles; everyone else yields.
  private separate(dt: number) {
    const sc = this.sepScratch
    const push = SEP_SPEED * dt
    for (let i = 0; i < this.stations.length; i++) {
      sc.length = 0
      for (let k = 0; k < this.sprites.length; k++) {
        const s = this.sprites[k]
        if (s.stationIndex === i && (s.mode === 'local' || s.mode === 'fadeIn') && !s.moving) sc.push(s)
      }
      if (sc.length < 2) continue
      this.idleBounds(i)
      for (let a = 0; a < sc.length; a++) {
        for (let b = a + 1; b < sc.length; b++) {
          const A = sc[a]
          const B = sc[b]
          const dx = B.x - A.x
          const dy = B.y - A.y
          if (!tooClose(dx, dy)) continue
          const fa = A.atWork || A.queued
          const fb = B.atWork || B.queued
          if (fa && fb) continue
          let nx = 1
          let ny = 0
          const d = Math.hypot(dx, dy)
          if (d > 0.01) { nx = dx / d; ny = dy / d }
          const m = fa || fb ? push : push / 2
          if (!fa) this.nudge(A, -nx * m, -ny * m)
          if (!fb) this.nudge(B, nx * m, ny * m)
        }
      }
    }
    sc.length = 0
  }

  private nudge(s: Sprite, dx: number, dy: number) {
    const x = Math.max(this.bMinX, Math.min(this.bMaxX, s.x + dx))
    const y = Math.max(this.bMinY, Math.min(this.bMaxY, s.y + dy))
    s.x = s.fromX = s.tx = x
    s.y = s.fromY = s.ty = y
  }

  /** Test introspection only: live particle count. */
  particleCount(): number {
    return this.particles.count()
  }

  // Finish everything instantly (reduced motion / static frame): tweens snap,
  // fades complete, walkers join their crew, blink/breath off.
  settle() {
    for (const s of this.sprites) {
      if (s.mode === 'fadeOut') continue
      if (s.mode === 'fadeIn') {
        s.mode = 'local'
        s.alpha = 1
      }
      if (s.moving) this.arrive(s)
      s.startT = 0
      s.settleT = 0
      s.blinkLeft = 0
      s.breathPhase = 0
      s.fidget = 0
      s.squashT = 0
    }
    this.sprites = this.sprites.filter((s) => s.mode !== 'fadeOut')
    this.reconcile()
    this.sprites = this.sprites.filter((s) => s.mode !== 'fadeOut')
    for (const s of this.sprites) { s.mode = 'local'; s.alpha = 1 }
    this.particles.clear()
    // Static emotes only: one "!" per gate station.
    this.clearEmotes()
    for (let i = 0; i < this.stations.length && i < this.emoteKind.length; i++) {
      if (!this.stations[i].gate) continue
      const o = this.pickEmoteOwner(i, false)
      if (o) {
        this.setEmote(i, 1, o, Infinity)
        this.emoteAge[i] = 1 // past the pop-in
      }
    }
  }

  step(dt: number) {
    this.aisle = (this.aisle + dt * 18) % 12
    this.clock += dt
    let anyGone = false
    for (const s of this.sprites) {
      const st = this.stations[s.stationIndex]
      if (!st) continue

      if (s.mode === 'fadeIn') {
        s.alpha = Math.min(1, s.alpha + dt / FADE_S)
        if (s.alpha >= 1) s.mode = 'local'
      } else if (s.mode === 'fadeOut') {
        s.alpha = Math.max(0, s.alpha - dt / FADE_S)
        if (s.alpha <= 0) anyGone = true
      }

      const act = this.poseAction(s, st)
      s.frameTimer += dt
      const fd = actionFrameDur(act) * s.jitter
      if (s.frameTimer >= fd) {
        s.frameTimer -= fd
        s.frame = (s.frame + 1) % actionFrameCount(act)
        s.tick++
      }
      if (s.burstCool > 0) s.burstCool -= dt
      if (s.squashT > 0) s.squashT -= dt
      if (s.frame !== s.prevFrame) {
        s.prevFrame = s.frame
        if (!s.moving && s.mode !== 'fadeOut' && s.alpha >= 1) this.emitFor(s, act)
      }
      if (s.mode === 'fadeOut') continue

      // A gate crew never queues for the workstation.
      if (st.gate && s.atWork && !s.moving) s.atWork = false
      if (st.gate && s.queued) { s.queued = false; s.qSlot = -1 }

      if (s.moving && s.startT > 0) {
        s.startT -= dt // anticipation: hold still on the pass pose
        if (s.startT <= 0) this.puff(s)
      } else if (s.moving) {
        s.t += dt
        const u = s.t >= s.dur ? 1 : s.t / s.dur
        const e = ease(u)
        const nx = s.fromX + (s.tx - s.fromX) * e
        const ny = s.fromY + (s.ty - s.fromY) * e
        // Feet follow distance covered, so eased starts/stops slow the steps.
        s.legDist += Math.hypot(nx - s.x, ny - s.y)
        while (s.legDist >= STRIDE) {
          s.legDist -= STRIDE
          s.leg = (s.leg + 1) % WALK_FRAME_COUNT
          s.steps++
        }
        s.x = nx
        s.y = ny
        if (u >= 1) {
          this.arrive(s)
          this.puff(s)
        }
      } else {
        // Idle life: breathing settle + occasional blink.
        if (s.settleT > 0) s.settleT -= dt
        s.breathPhase += dt
        if (s.blinkLeft > 0) s.blinkLeft -= dt
        else {
          s.blinkTimer -= dt
          if (s.blinkTimer <= 0) {
            s.blinkLeft = 0.12
            s.blinkTimer = rand(2, 5)
          }
        }
        if (!s.queued) {
          s.pause -= dt
          if (s.pause <= 0) {
            const i = s.stationIndex
            if (s.atWork) {
              // Done working: yield the slot and step aside to idle.
              s.atWork = false
              if (st.action === 'drawing') this.setEmote(i, 3, s, 1.4)
              else if (st.action === 'approving') this.setEmote(i, 4, s, 1.4)
              const t = this.idleTarget(i, s)
              this.startMove(s, t.x, t.y)
            } else if (!st.gate && !this.slotTaken(i) && !this.anyQueued(i)) {
              // Slot free and nobody ahead: claim it and walk up to the workstation.
              s.atWork = true
              this.workSlotXY(i)
              this.startMove(s, this.tmpX, this.tmpY)
            } else if (!st.gate) {
              // Slot busy: join the line (updateQueues gives it a place).
              s.queued = true
              s.ticket = ++this.ticketSeq
              s.waitT = 0
              s.qSlot = -1
              s.frameTimer = Math.random() * actionFrameDur('idle') * s.jitter
            } else {
              // Human gate: mill around and re-check next pause.
              const t = this.idleTarget(i, s)
              this.startMove(s, t.x, t.y)
            }
          }
        }
        if (!s.moving && !s.atWork && !st.gate && s.mode === 'local' && s.settleT <= 0) this.stepFidget(s, dt)
      }
    }
    if (anyGone) this.sprites = this.sprites.filter((s) => !(s.mode === 'fadeOut' && s.alpha <= 0))
    this.updateQueues(dt)
    this.separate(dt)
    this.stepEmotes(dt)
    this.emitStations(dt)
    this.particles.step(dt)
  }

  // ---- particle emission ----------------------------------------------------
  // Everything below writes plain numbers into the pooled arrays: no objects
  // or arrays are created per particle.

  // Workstation origin/size for station i, without allocating (see workstation()).
  private wsBox(i: number) {
    const col = i % this.cols
    const row = Math.floor(i / this.cols)
    this.tmpX = PAD + col * STATION_W + STATION_W / 2 - 42 // ws.x
    this.tmpY = PAD + row * CELL_H + SIGN_H + 12 // ws.y
  }

  // Sparks / paper / confetti when a parked sprite's action frame changes.
  private emitFor(s: Sprite, act: Action) {
    const pl = this.particles
    if (act === 'hammering' && isImpactFrame(act, s.frame, s.dir)) {
      let x: number
      let y: number
      if (s.atWork) {
        this.wsBox(s.stationIndex)
        x = this.tmpX + 42
        y = this.tmpY + 46 * 0.42
      } else {
        x = s.dir === 'side' && s.facing === -1 ? s.x + SPRITE_W - 13 * ART_PX : s.x + 13 * ART_PX
        y = s.y + 17 * ART_PX
      }
      const n = 4 + Math.floor(Math.random() * 3)
      for (let k = 0; k < n; k++) {
        pl.spawn(SPARK, x, y, rand(-70, 70), rand(-140, -50), 300, rand(0.25, 0.45), Math.floor(Math.random() * 3))
      }
    } else if (act === 'drawing' && Math.random() < 0.3) {
      let x: number
      let y: number
      if (s.atWork) {
        this.wsBox(s.stationIndex)
        x = this.tmpX + 84 * 0.25
        y = this.tmpY + 46 * 0.7
      } else {
        x = s.x + 11 * ART_PX
        y = s.y + 15 * ART_PX
      }
      pl.spawn(PAPER, x, y, 0, 18, 0, 1.2, 3 + Math.floor(Math.random() * 2))
    } else if (act === 'celebrating' && s.burstCool <= 0 && isImpactFrame(act, s.frame, s.dir)) {
      s.burstCool = 0.9
      const x = s.x + SPRITE_W / 2
      const y = s.y + 2 * ART_PX
      const n = 10 + Math.floor(Math.random() * 5)
      for (let k = 0; k < n; k++) {
        pl.spawn(CONFETTI, x, y, rand(-70, 70), rand(-150, -60), 260, rand(0.8, 1.4), 11 + Math.floor(Math.random() * 5))
      }
    }
  }

  // Steam puffs and blinking LEDs on the server rack of robot stations.
  private emitStations(dt: number) {
    const e = this.stationEmit
    for (let i = 0; i < this.stations.length; i++) {
      const st = this.stations[i]
      if (st.gate || st.action !== 'robot' || st.count <= 0) continue
      this.wsBox(i)
      const wx = this.tmpX
      const wy = this.tmpY
      const sx = 84 / 60
      const sy = 46 / 34
      e[i * 2] -= dt
      if (e[i * 2] <= 0) {
        e[i * 2] = 0.35
        this.particles.spawn(STEAM, wx + (30 + rand(-10, 10)) * sx, wy - 2, rand(-6, 6), -22, 0, 1.5, 5 + Math.floor(Math.random() * 3))
      }
      e[i * 2 + 1] -= dt
      if (e[i * 2 + 1] <= 0) {
        e[i * 2 + 1] = 0.2
        const k = Math.floor(Math.random() * 4)
        // Four fixed LED spots on the rack face.
        const lx = 30 + (k % 2 === 0 ? -9 : 8)
        const ly = 8 + (k >> 1) * 8 + (k === 3 ? 6 : 0)
        this.particles.spawn(LED, wx + lx * sx, wy + ly * sy, 0, 0, 0, 0.2, 8 + Math.floor(Math.random() * 3))
      }
    }
  }


  // ---- lazily-built cached layers -------------------------------------------
  // Everything static (floor, props, workstations, signs, lamps) is baked into
  // an offscreen canvas at device resolution; the lights are baked at ART-PIXEL
  // resolution (1 canvas px = 1 art px) so they stay crisp at every zoom. All
  // building happens inside draw() on demand (never in layout/setStations), so
  // nothing is created per frame and tests without a canvas never touch one.

  private makeLayer(w: number, h: number): CanvasRenderingContext2D | null {
    const c = this.canvasFactory(Math.max(1, w), Math.max(1, h))
    if (!c) return null
    const g = c.getContext('2d') as CanvasRenderingContext2D | null
    if (!g) return null
    this.lastLayer = c
    return g
  }

  private ensureLayers(ctx: CanvasRenderingContext2D, cssW: number, cssH: number) {
    if (this.bgDirty || this.bgW !== cssW || this.bgH !== cssH) this.buildBg(ctx, cssW, cssH)
    if (this.lightDirty) this.buildLight()
  }

  private buildBg(ctx: CanvasRenderingContext2D, cssW: number, cssH: number) {
    this.bgDirty = false
    this.bgW = cssW
    this.bgH = cssH
    this.bg = null
    this.front = null
    const dpr = (typeof ctx.getTransform === 'function' ? ctx.getTransform().a : 1) || 1
    const pw = Math.ceil(cssW * dpr)
    const ph = Math.ceil(cssH * dpr)
    const k = dpr * this.scale
    const bgCtx = this.makeLayer(pw, ph)
    if (!bgCtx) return
    const bgCanvas = this.lastLayer
    bgCtx.imageSmoothingEnabled = false
    bgCtx.scale(k, k)
    this.paintBackground(bgCtx, cssW / this.scale, cssH / this.scale)
    const frCtx = this.makeLayer(pw, ph)
    if (!frCtx) return
    frCtx.imageSmoothingEnabled = false
    frCtx.scale(k, k)
    this.paintFront(frCtx)
    this.bg = bgCanvas
    this.front = this.lastLayer
  }

  private paintBackground(ctx: CanvasRenderingContext2D, w: number, h: number) {
    // ONE continuous warm wood floor across the whole scene. Stations abut with
    // no gap, so with a single floor the whole thing reads as one open room.
    drawSharedFloor(ctx, w, h)
    // Short back-props (behind the desk), then workstations + signs.
    this.stations.forEach((st, i) => {
      const p = this.patch(i)
      drawScatter(ctx, p, mulberry32(0x9e37 + i * 2654435761), 'back', 'noLamp')
      const props = this.sheets?.props
      if (props) drawPropWorkstation(ctx, props as CanvasImageSource, st.action, this.workstation(i))
      else drawWorkstation(ctx, st.action, this.workstation(i), st.color)
      drawSign(ctx, this.workstation(i), st)
    })
    // Tall floor lamps AFTER workstations (so a lamp beside a desk isn't
    // clipped by it) but before sprites. Same seed → same positions.
    this.stations.forEach((_st, i) => {
      drawScatter(ctx, this.patch(i), mulberry32(0x9e37 + i * 2654435761), 'back', 'lampOnly')
    })
  }

  // Front-props: near the bottom edge, drawn over sprites (a plant a worker
  // walks behind), grounding the room.
  private paintFront(ctx: CanvasRenderingContext2D) {
    this.stations.forEach((_st, i) => {
      drawScatter(ctx, this.patch(i), mulberry32(0x51ed + i * 2654435761), 'front')
    })
  }

  private buildLight() {
    this.lightDirty = false
    this.shade = null
    this.glow = null
    const rows = Math.max(1, Math.ceil((this.stations.length || 1) / Math.max(1, this.cols)))
    const lw = Math.ceil(this.layoutW / ART_PX)
    const lh = Math.ceil((PAD * 2 + rows * CELL_H) / ART_PX)
    this.lightW = lw
    this.lightH = lh

    const sg = this.makeLayer(lw, lh)
    if (!sg) return
    const shadeCanvas = this.lastLayer
    sg.fillStyle = 'rgba(16,10,32,0.18)' // dusk ambient
    sg.fillRect(0, 0, lw, lh)
    const vig = sg.createRadialGradient(lw / 2, lh / 2, 0, lw / 2, lh / 2, Math.hypot(lw / 2, lh / 2))
    vig.addColorStop(0, 'rgba(8,4,16,0)')
    vig.addColorStop(0.55, 'rgba(8,4,16,0)')
    vig.addColorStop(1, 'rgba(8,4,16,0.3)')
    sg.fillStyle = vig
    sg.fillRect(0, 0, lw, lh)
    // Cut a soft hole in the shade around every workstation.
    sg.globalCompositeOperation = 'destination-out'
    const rad = 80 / ART_PX
    this.stations.forEach((_st, i) => {
      const ws = this.workstation(i)
      const cx = (ws.x + ws.w / 2) / ART_PX
      const cy = (ws.y + ws.h / 2) / ART_PX
      const g = sg.createRadialGradient(cx, cy, 0, cx, cy, rad)
      g.addColorStop(0, 'rgba(0,0,0,0.9)')
      g.addColorStop(1, 'rgba(0,0,0,0)')
      sg.fillStyle = g
      sg.fillRect(cx - rad, cy - rad, rad * 2, rad * 2)
    })
    sg.globalCompositeOperation = 'source-over'

    const gg = this.makeLayer(lw, lh)
    if (!gg) return
    const glowCanvas = this.lastLayer
    this.stations.forEach((st, i) => {
      const ws = this.workstation(i)
      const cx = (ws.x + ws.w / 2) / ART_PX
      const cy = (ws.y + ws.h / 2) / ART_PX
      const [r, g, b] = mixHex('#ffd59a', st.color, 0.25)
      const grad = gg.createRadialGradient(cx, cy, 0, cx, cy, rad)
      grad.addColorStop(0, `rgba(${r},${g},${b},0.22)`)
      grad.addColorStop(1, `rgba(${r},${g},${b},0)`)
      gg.fillStyle = grad
      gg.fillRect(cx - rad, cy - rad, rad * 2, rad * 2)
      // Small glow at each floor lamp (positions come from the scatter seed).
      for (const pr of scatterProps(this.patch(i), mulberry32(0x9e37 + i * 2654435761), 'back')) {
        if (pr.kind !== 'lamp') continue
        const lx = (pr.x + 7) / ART_PX
        const ly = (pr.y + 4) / ART_PX
        const lr = 36 / ART_PX
        const lg = gg.createRadialGradient(lx, ly, 0, lx, ly, lr)
        lg.addColorStop(0, 'rgba(253,224,138,0.2)')
        lg.addColorStop(1, 'rgba(253,224,138,0)')
        gg.fillStyle = lg
        gg.fillRect(lx - lr, ly - lr, lr * 2, lr * 2)
      }
    })
    this.shade = shadeCanvas
    this.glow = glowCanvas
  }

  private drawParticles(ctx: CanvasRenderingContext2D, emissive: boolean) {
    const p = this.particles
    let alpha = 1
    for (let i = 0; i < p.cap; i++) {
      if (!p.alive[i]) continue
      const k = p.kind[i]
      const em = k !== PAPER && k !== STEAM && k !== DUST
      if (em !== emissive) continue
      let a = 1
      if (!em) a = (Math.ceil((p.life[i] / p.maxLife[i]) * 3) / 3) * (k === STEAM ? 0.55 : k === DUST ? 0.8 : 0.9)
      if (a !== alpha) {
        ctx.globalAlpha = a
        alpha = a
      }
      ctx.fillStyle = PARTICLE_COLORS[p.color[i]]
      const sz = (k === STEAM ? 2 : 1) * ART_PX
      ctx.fillRect(Math.round(p.x[i] / ART_PX) * ART_PX, Math.round(p.y[i] / ART_PX) * ART_PX, sz, sz)
    }
    if (alpha !== 1) ctx.globalAlpha = 1
  }

  draw(ctx: CanvasRenderingContext2D, cssW: number, cssH: number) {
    const scale = this.scale
    ctx.imageSmoothingEnabled = false
    ctx.clearRect(0, 0, cssW, cssH)
    this.ensureLayers(ctx, cssW, cssH)
    ctx.save()
    ctx.scale(scale, scale)
    const w = cssW / scale
    const h = cssH / scale

    // 1. static background (cached layer, or direct drawing if no canvas).
    if (this.bg) ctx.drawImage(this.bg as CanvasImageSource, 0, 0, w, h)
    else this.paintBackground(ctx, w, h)

    // 1b. ambient shade + vignette on the floor only, smoothly upscaled, so
    // it never dithers over the characters walking through it.
    if (this.shade) {
      ctx.imageSmoothingEnabled = true
      ctx.drawImage(this.shade as CanvasImageSource, 0, 0, this.lightW * ART_PX, this.lightH * ART_PX)
      ctx.imageSmoothingEnabled = false
    }

    // 2. depth-sorted sprites (reused array: no per-frame allocation).
    const ordered = this.ordered
    ordered.length = 0
    for (let i = 0; i < this.sprites.length; i++) ordered.push(this.sprites[i])
    ordered.sort(byY)
    for (let i = 0; i < ordered.length; i++) this.drawOne(ctx, ordered[i], scale)

    // 3. front props.
    if (this.front) ctx.drawImage(this.front as CanvasImageSource, 0, 0, w, h)
    else this.paintFront(ctx)

    // 4. particles: paper + steam sit under the light, sparks/LEDs/confetti
    // are emissive and go over it.
    const live = this.particles.count() > 0
    if (live) this.drawParticles(ctx, false)

    // 5. warm light pools, additive and smoothly upscaled (soft, no dither).
    if (this.glow) {
      ctx.imageSmoothingEnabled = true
      ctx.globalCompositeOperation = 'lighter'
      ctx.drawImage(this.glow as CanvasImageSource, 0, 0, this.lightW * ART_PX, this.lightH * ART_PX)
      ctx.globalCompositeOperation = 'source-over'
      ctx.imageSmoothingEnabled = false
    }
    if (live) this.drawParticles(ctx, true)

    // 6. overlay last so the shade never dims it: emote bubbles and the
    // "+N" tag by an overfull station's line.
    for (let i = 0; i < this.stations.length; i++) {
      const st = this.stations[i]
      const label = this.overflowLabel[i]
      if (label) this.drawQueueTag(ctx, i, label)
      const kind = i < this.emoteKind.length ? this.emoteKind[i] : 0
      const s = kind ? this.emoteOwner[i] : null
      if (!s) continue
      const sx = Math.round(s.x * scale) / scale
      const sy = Math.round(s.y * scale) / scale
      const age = this.emoteAge[i]
      const rem = this.emoteLife[i] - age
      let a = s.alpha
      if (rem < EMOTE_FADE) a *= rem > EMOTE_FADE / 2 ? 2 / 3 : 1 / 3
      if (a !== 1) ctx.globalAlpha = a
      const hop = kind === 1 && Math.floor(this.clock * 2.5) % 2 ? -ART_PX : 0
      // Sheet workers are taller than the grid box: lift the bubble over the head.
      const lift = this.sheets && !this.robots && actionOf(st) !== 'robot' ? 30 : 16
      drawEmote(ctx, kind, sx + SPRITE_W / 2, sy - lift - 3 * ART_PX + hop, age < EMOTE_POP ? ART_PX / 2 : ART_PX, st.color)
      if (a !== 1) ctx.globalAlpha = 1
    }
    ctx.restore()
  }

  // Small "+N" chip beside the tail of the waiting line.
  private drawQueueTag(ctx: CanvasRenderingContext2D, i: number, label: string) {
    this.queueSlotPos(QUEUE_OFFSETS.length - 1, i)
    const w = label.length * 5 + 7
    const x = Math.round(this.tmpX + SPRITE_W / 2 + 20)
    const y = Math.round(this.tmpY + SPRITE_H - 13)
    ctx.fillStyle = '#0f172a'
    ctx.fillRect(x, y, w, 11)
    ctx.strokeStyle = 'rgba(148,163,184,0.35)'
    ctx.lineWidth = 1
    ctx.strokeRect(x + 0.5, y + 0.5, w - 1, 10)
    ctx.fillStyle = '#94a3b8'
    ctx.font = '700 8px ui-sans-serif, system-ui, sans-serif'
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(label, x + w / 2, y + 6)
  }

  private drawOne(ctx: CanvasRenderingContext2D, s: Sprite, scale: number) {
    const st = this.stations[s.stationIndex]
    if (!st) return
    this.resolvePose(s, st)
    const act = this.pAct
    const dir = this.pDir
    const facing = this.pFacing
    const fidx = this.pIdx >= 0 ? this.pIdx : s.frame
    const robotLook = this.robots || actionOf(st) === 'robot'
    const parked = !s.moving
    // Pose: walk cycle while moving (after a short anticipation step), a
    // feet-together settle after arriving, else the action frame.
    let walkLeg = -1
    let bob = 0
    if (s.moving) {
      if (s.startT > 0) {
        walkLeg = 1
      } else {
        walkLeg = s.leg
        // Bob only at cruising speed (ease speed ∝ sin(πu)): first/last steps are flat.
        const u = s.dur > 0 ? Math.min(1, s.t / s.dur) : 1
        if (Math.sin(Math.PI * u) > 0.35) bob = robotLook ? ROBOT_HOVER_BOB[s.leg % ROBOT_HOVER_BOB.length] : WALK_BOB[s.leg % WALK_BOB.length]
      }
    } else if (s.settleT > 0) {
      walkLeg = 1
    } else {
      bob = actionBob(act, fidx)
    }
    const blink = parked && walkLeg < 0 && s.blinkLeft > 0
    const breath = parked && Math.sin((2 * Math.PI * s.breathPhase) / BREATH_PERIOD) > 0.6
    const carry = s.mode === 'handoff' && s.moving
    if (this.sheets) {
      this.drawFromSheet(ctx, s, act, dir, facing, this.pIdx, robotLook, scale, s.moving ? 0 : bob, carry)
      return
    }
    const flags = (blink ? 1 : 0) | (breath ? 2 : 0) | (carry ? 4 : 0) | (this.robots ? 8 : 0)
    let f = s.lastFrame
    if (!f || s.fAct !== act || s.fIdx !== fidx || s.fLeg !== walkLeg || s.fDir !== dir || s.fFlags !== flags) {
      f = composeFrame(act, fidx, walkLeg, s.variant, dir, this.robots, { blink, breath, carry })
      s.lastFrame = f
      s.fAct = act
      s.fIdx = fidx
      s.fLeg = walkLeg
      s.fDir = dir
      s.fFlags = flags
    }
    // 'side' is authored facing right; mirror for leftward travel.
    const flip = dir === 'side' && facing === -1
    // Snap the origin to whole CSS px so every art pixel lands on integer px.
    const sx = Math.round(s.x * scale) / scale
    const sy = Math.round(s.y * scale) / scale
    if (s.alpha < 1) ctx.globalAlpha = s.alpha
    drawSprite(ctx, f, sx, sy, bob * ART_PX, flip ? -1 : 1, robotLook && s.squashT > 0)
    if (s.alpha < 1) ctx.globalAlpha = 1
  }

  private drawFromSheet(
    ctx: CanvasRenderingContext2D, s: Sprite, act: Action, dir: Dir, facing: 1 | -1, fidx: number,
    robotLook: boolean, scale: number, bob: number, carry: boolean,
  ) {
    const sheets = this.sheets!
    const walking = s.moving && s.startT <= 0
    // A queued robot-station sprite waits on the robot's idle rows.
    const pa = robotLook && act === 'robot' && s.queued && !s.moving ? 'idle' : act
    const clip = pickClip({ action: pa, dir, moving: walking, carry, robots: robotLook, alt: s.jitter > 1.1 })
    const spec = clip.sheet === 'worker' ? WORKER_SHEET : ROBOT_SHEET
    const img = (clip.sheet === 'worker' ? sheets.workerFor(s.variant) : sheets.robot) as CanvasImageSource
    const idx = (walking ? s.steps : fidx >= 0 ? fidx : s.tick) % clip.frames
    const sx = Math.round(s.x * scale) / scale
    const sy = Math.round(s.y * scale) / scale
    const footX = sx + SPRITE_W / 2
    const footY = sy + SPRITE_H
    const dw = spec.cellW * SHEET_PX
    const dh = spec.cellH * SHEET_PX
    const dy = footY + bob * ART_PX - spec.baseline * SHEET_PX
    if (s.alpha < 1) ctx.globalAlpha = s.alpha
    drawGroundShadow(ctx, footX, footY, Math.round(-bob))
    const squash = robotLook && s.squashT > 0
    if (squash) {
      // 1 art px shorter, 1 wider, pivoting on the feet.
      ctx.save()
      ctx.translate(footX, footY)
      ctx.scale((dw + ART_PX) / dw, (dh - ART_PX) / dh)
      ctx.translate(-footX, -footY)
    }
    ctx.save()
    ctx.translate(footX, 0)
    if (dir === 'side' && facing === -1) ctx.scale(-1, 1)
    ctx.drawImage(img, idx * spec.cellW, clip.row * spec.cellH, spec.cellW, spec.cellH, -dw / 2, dy, dw, dh)
    ctx.restore()
    if (squash) ctx.restore()
    if (s.alpha < 1) ctx.globalAlpha = 1
  }
}

// Base units per PNG-sheet pixel. The sheets are drawn at a finer pixel than
// the grid art (ART_PX = 3) so the ~42px-tall worker stands about as tall as
// the old 24-row sprite plus a head, next to the same workstations.
const SHEET_PX = 2

type CanvasLike = { width: number; height: number; getContext(type: '2d'): unknown }

function byY(a: Sprite, b: Sprite): number {
  return a.y - b.y
}

// Parse #rgb / #rrggbb; null for anything else (labels carry user colors).
function parseHex(c: string): [number, number, number] | null {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(c.trim())
  if (!m) return null
  let h = m[1]
  if (h.length === 3) h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2]
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)]
}

/** Mix `a` toward `b` by t (0..1); falls back to `a` if either isn't a hex color. */
export function mixHex(a: string, b: string, t: number): [number, number, number] {
  const pa = parseHex(a) ?? [255, 213, 154]
  const pb = parseHex(b)
  if (!pb) return pa
  return [0, 1, 2].map((i) => Math.round(pa[i] * (1 - t) + pb[i] * t)) as [number, number, number]
}

// Soft ground shadow: a stepped ellipse of whole-art-pixel rows centered on
// `cx`, bottom row at `footY`. Shrinks and fades as the body lifts (0..2 art px).
const SHADOW_FILL = SHADOW_ALPHA.map((a) => `rgba(0,0,0,${a})`)
function drawGroundShadow(ctx: CanvasRenderingContext2D, cx: number, footY: number, lift: number) {
  const li = lift < 0 ? 0 : lift > 2 ? 2 : lift
  const rows = SHADOW_ROWS[li]
  ctx.fillStyle = SHADOW_FILL[li]
  for (let i = 0; i < rows.length; i++) {
    const hw = rows[i] * ART_PX
    ctx.fillRect(cx - hw, footY - (rows.length - 1 - i) * ART_PX, hw * 2, ART_PX)
  }
}

// Tiny deterministic PRNG so a station's scatter props stay put frame-to-frame
// (seeded by station index), instead of jumping every render.
function mulberry32(seed: number) {
  let a = seed >>> 0
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

// One continuous warm wood floor spanning the whole canvas. Uniform planks so
// abutting stations merge into a single open room. Seeded speckle adds subtle
// grain without per-station seams.
const FLOOR = '#6b4f34'
const FLOOR_LINE = '#5c432c'
function drawSharedFloor(ctx: CanvasRenderingContext2D, w: number, h: number) {
  ctx.fillStyle = FLOOR
  ctx.fillRect(0, 0, w, h)
  ctx.fillStyle = FLOOR_LINE
  for (let y = 12; y < h; y += 14) ctx.fillRect(0, y, w, 1) // planks
  // Faint grain speckle.
  const rng = mulberry32(0x1234)
  ctx.globalAlpha = 0.06
  ctx.fillStyle = '#000000'
  for (let k = 0; k < (w * h) / 900; k++) ctx.fillRect(rng() * w, rng() * h, 1, 1)
  ctx.globalAlpha = 1
}

// The name + count plaque, mounted just above the workstation in the SIGN_H
// band. Tinted to the stage's accent color so stages stay distinguishable now
// that the floor is uniform. Shows a "+N" overflow suffix when capped.
function drawSign(
  ctx: CanvasRenderingContext2D,
  ws: { x: number; y: number; w: number; h: number },
  st: Station,
) {
  const label = st.name.toUpperCase()
  ctx.font = '700 10px ui-sans-serif, system-ui, sans-serif'
  const labelW = ctx.measureText(label).width
  const count = String(st.count)
  ctx.font = '800 13px ui-sans-serif, system-ui, sans-serif'
  const countW = ctx.measureText(count).width
  const overflow = st.count - CAP
  const extra = overflow > 0 ? `+${overflow}` : ''

  const w = Math.max(52, labelW + 16, countW + 26)
  const h = 24
  const x = Math.round(ws.x + ws.w / 2 - w / 2)
  const y = Math.round(ws.y - h - 4)

  // Plaque: dark panel with an accent top bar + a little post to the desk.
  ctx.fillStyle = 'rgba(0,0,0,0.28)'; ctx.fillRect(x + 2, y + h, w - 4, 3) // shadow
  ctx.fillStyle = '#8a6c44'; ctx.fillRect(x + w / 2 - 1, y + h, 2, 6) // mounting post
  ctx.fillStyle = '#0f172a'; ctx.fillRect(x, y, w, h)
  ctx.fillStyle = st.color; ctx.fillRect(x, y, w, 4) // accent bar
  ctx.strokeStyle = 'rgba(148,163,184,0.35)'; ctx.lineWidth = 1
  ctx.strokeRect(x + 0.5, y + 0.5, w - 1, h - 1)

  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillStyle = '#cbd5e1'
  ctx.font = '700 9px ui-sans-serif, system-ui, sans-serif'
  ctx.fillText(label, x + w / 2, y + 10)
  ctx.fillStyle = '#f1f5f9'
  ctx.font = '800 13px ui-sans-serif, system-ui, sans-serif'
  ctx.fillText(count, x + w / 2, y + 18)
  if (extra) {
    ctx.fillStyle = '#94a3b8'
    ctx.font = '700 8px ui-sans-serif, system-ui, sans-serif'
    ctx.textAlign = 'left'
    ctx.fillText(extra, x + w / 2 + countW / 2 + 2, y + 19)
    ctx.textAlign = 'center'
  }
}

type Prop = { kind: 'lamp' | 'plant' | 'box' | 'mug'; x: number; y: number }

// The stable prop list for a band of a patch. The rng draw sequence is fixed
// (count, then x/y/pick per prop), so positions/types never change between
// the draw phases and the light-layer build.
function scatterProps(p: Patch, rng: () => number, band: 'back' | 'front'): Prop[] {
  const n = band === 'back' ? 2 + Math.floor(rng() * 2) : 1 + Math.floor(rng() * 2)
  const yMin = band === 'back' ? p.y + 16 : p.y + p.h - 14
  const ySpan = band === 'back' ? p.h * 0.22 : 10
  const out: Prop[] = []
  for (let k = 0; k < n; k++) {
    const x = p.x + 4 + rng() * (p.w - 20)
    const y = yMin + rng() * ySpan
    const pick = rng()
    // The floor lamp is tall (~character height); it only fits in the back band
    // (full patch height below it), so in the front band that slot is a plant.
    let kind: Prop['kind']
    if (pick < 0.65) kind = pick >= 0.45 && band === 'back' ? 'lamp' : 'plant'
    else kind = pick < 0.85 ? 'box' : 'mug'
    out.push({ kind, x, y })
  }
  return out
}

// Scatter a stable set of small props (plants, boxes, mugs, lamps) in a band of
// the patch. 'back' fills the upper third; 'front' the bottom edge.
//
// The `only` phase controls which props render:
//   'noLamp' — everything except the tall floor lamp (drawn before workstations
//              so short props sit behind the desk)
//   'lampOnly' — only the floor lamp (drawn AFTER workstations so a tall lamp
//                standing next to a desk isn't clipped by it), back band only
//   'all' — used for the front band (no lamps there anyway)
function drawScatter(
  ctx: CanvasRenderingContext2D,
  p: Patch,
  rng: () => number,
  band: 'back' | 'front',
  only: 'noLamp' | 'lampOnly' | 'all' = 'all',
) {
  for (const pr of scatterProps(p, rng, band)) {
    if (pr.kind === 'lamp') {
      if (only !== 'noLamp') drawLamp(ctx, pr.x, pr.y)
      continue
    }
    if (only === 'lampOnly') continue
    if (pr.kind === 'plant') drawPlant(ctx, pr.x, pr.y)
    else if (pr.kind === 'box') drawBox(ctx, pr.x, pr.y)
    else drawMug(ctx, pr.x, pr.y)
  }
}

// Two-row stepped contact shadow: darker inner row, lighter wider outer row.
function propShadow(ctx: CanvasRenderingContext2D, x: number, y: number, w: number) {
  ctx.fillStyle = 'rgba(0,0,0,0.12)'
  ctx.fillRect(x - 3, y + 2, w + 6, 2)
  ctx.fillStyle = 'rgba(0,0,0,0.25)'
  ctx.fillRect(x - 1, y, w + 2, 2)
}

// A potted plant: terracotta pot with a rim, and pointed leaves fanning out in
// light/mid/dark greens so it reads as a plant, not a green blob. Drawn ~1.5x
// the earlier size (≈17 wide x 26 tall) so it holds its own on the floor.
function drawPlant(ctx: CanvasRenderingContext2D, x: number, y: number) {
  propShadow(ctx, x + 1, y + 25, 15)
  // Terracotta pot (tapered) with a lighter rim.
  ctx.fillStyle = '#a1552b'; ctx.fillRect(x + 3, y + 17, 13, 9) // body
  ctx.fillStyle = '#8a4522'; ctx.fillRect(x + 4, y + 23, 11, 3) // base shade
  ctx.fillStyle = '#c26a38'; ctx.fillRect(x + 1, y + 15, 17, 3) // rim
  ctx.fillStyle = '#d67d47'; ctx.fillRect(x + 1, y + 15, 17, 1) // rim highlight
  // Foliage: a central stalk with pointed leaves fanning out.
  const dark = '#15803d'
  const mid = '#22c55e'
  const lite = '#4ade80'
  ctx.fillStyle = dark
  ctx.fillRect(x + 1, y + 9, 3, 7) // lower-left leaf
  ctx.fillRect(x + 14, y + 9, 3, 7) // lower-right leaf
  ctx.fillRect(x + 7, y + 6, 3, 9) // center stalk (behind)
  ctx.fillStyle = mid
  ctx.fillRect(x + 3, y + 4, 3, 6); ctx.fillRect(x + 4, y + 2, 2, 3) // left leaf
  ctx.fillRect(x + 12, y + 4, 3, 6); ctx.fillRect(x + 12, y + 2, 2, 3) // right leaf
  ctx.fillRect(x + 7, y + 5, 3, 9) // center stalk (front)
  ctx.fillStyle = lite
  ctx.fillRect(x + 7, y, 3, 6) // center leaf highlight
  ctx.fillRect(x + 4, y + 5, 1, 2); ctx.fillRect(x + 13, y + 5, 1, 2) // side glints
}

// A stand-up floor lamp: tall slim pole on a small base with a warm glowing
// shade and a soft light halo, mixed into the scatter for a cozier room.
// Sized close to a worker's height (~56px, vs the 72px sprite) so it reads as
// a real floor lamp standing among them rather than a tabletop lamp.
function drawLamp(ctx: CanvasRenderingContext2D, x: number, y: number) {
  propShadow(ctx, x + 2, y + 56, 9)
  ctx.fillStyle = 'rgba(0,0,0,0.14)'
  ctx.fillRect(x + 9, y + 57, 10, 2) // longer offset shadow
  // Base + long pole.
  ctx.fillStyle = '#334155'; ctx.fillRect(x + 3, y + 54, 8, 2) // base
  ctx.fillStyle = '#475569'; ctx.fillRect(x + 6, y + 8, 2, 46) // pole
  // Soft warm halo around the shade.
  ctx.save()
  ctx.globalAlpha = 0.18
  ctx.fillStyle = '#fde68a'
  ctx.fillRect(x, y - 2, 14, 12)
  ctx.globalAlpha = 0.1
  ctx.fillRect(x - 2, y - 4, 18, 16)
  ctx.restore()
  // Conical shade (trapezoid from rects) with a lit underside.
  ctx.fillStyle = '#e2b04a'; ctx.fillRect(x + 3, y, 8, 2) // shade top
  ctx.fillStyle = '#f4cf74'; ctx.fillRect(x + 2, y + 2, 10, 3) // shade mid
  ctx.fillStyle = '#fde9a8'; ctx.fillRect(x + 1, y + 5, 12, 2) // shade rim (bright, lit)
}

function drawBox(ctx: CanvasRenderingContext2D, x: number, y: number) {
  propShadow(ctx, x, y + 10, 10)
  ctx.fillStyle = '#c8a06a'; ctx.fillRect(x, y + 2, 10, 8)
  ctx.fillStyle = '#a8834f'; ctx.fillRect(x, y + 2, 10, 1); ctx.fillRect(x + 4, y + 2, 2, 8)
}

function drawMug(ctx: CanvasRenderingContext2D, x: number, y: number) {
  propShadow(ctx, x, y + 6, 5)
  ctx.fillStyle = '#e2e8f0'; ctx.fillRect(x, y + 1, 5, 5)
  ctx.fillStyle = '#94a3b8'; ctx.fillRect(x + 5, y + 2, 1, 2) // handle
}

// Draw the shared workstation for a station's action. Each is a small
// top-down pixel prop drawn from rects; the accent color tints its surface so
// it reads as belonging to that station.
// Props are authored against this base footprint; the actual (larger) ws size
// is reached by scaling, so every internal offset grows proportionally and we
// don't have to re-tune ~25 rects.
const WS_BASE_W = 60
const WS_BASE_H = 34

// Stepped soft shadow under a workstation's front edge, drawn in whole art
// pixels OUTSIDE the non-integer workstation scale so it stays snapped. Tall
// props (server rack, podium) get a longer, darker one.
function contactShadow(ctx: CanvasRenderingContext2D, ws: { x: number; y: number; w: number; h: number }, tall: boolean) {
  const snap = (v: number) => Math.round(v / ART_PX) * ART_PX
  const x0 = snap(ws.x)
  const x1 = snap(ws.x + ws.w)
  const y = snap(ws.y + ws.h)
  ctx.fillStyle = tall ? 'rgba(0,0,0,0.2)' : 'rgba(0,0,0,0.14)'
  ctx.fillRect(x0 - ART_PX, y + ART_PX, x1 - x0 + ART_PX * 2, ART_PX) // outer
  ctx.fillStyle = tall ? 'rgba(0,0,0,0.3)' : 'rgba(0,0,0,0.22)'
  ctx.fillRect(x0, y, x1 - x0, ART_PX) // inner
  if (tall) {
    ctx.fillStyle = 'rgba(0,0,0,0.12)'
    ctx.fillRect(x0 + ART_PX, y + ART_PX * 2, x1 - x0 - ART_PX * 2, ART_PX)
  }
}

// Base units per furniture-prop pixel. Props are drawn a bit finer than the
// crew (SHEET_PX = 2) so a desk sits about waist high next to a worker; at
// the default zoom on a 2x screen that is a whole 3 device px per pixel.
const PROP_PX = 1.5

// Draw one atlas prop bottom-centred at (cx, footY) in base units.
function drawPropAt(ctx: CanvasRenderingContext2D, props: CanvasImageSource, name: PropName, cx: number, footY: number) {
  const [x, y, w, h] = PROP_RECTS[name]
  const dw = w * PROP_PX
  const dh = h * PROP_PX
  const dx = Math.round(cx - dw / 2)
  const dy = Math.round(footY - dh)
  propShadow(ctx, dx + 2, Math.round(footY) - 2, Math.round(dw) - 4)
  ctx.drawImage(props, x, y, w, h, dx, dy, dw, dh)
}

// PNG furniture for a station, standing on the workstation footprint's front
// edge where the hand-drawn prop sits.
function drawPropWorkstation(
  ctx: CanvasRenderingContext2D,
  props: CanvasImageSource,
  action: Action,
  ws: { x: number; y: number; w: number; h: number },
) {
  const name = propFor(action)
  // A few units below the footprint's edge so the plaque above stays clear.
  const footY = ws.y + ws.h + PROP_DROP
  drawPropAt(ctx, props, name, ws.x + ws.w / 2, footY)
  // The break corner: a coffee machine beside the not-ready desk and a water
  // cooler beside the review desk.
  if (name === 'idle') drawPropAt(ctx, props, 'coffee', ws.x + ws.w + 20, footY)
  else if (name === 'inspecting') drawPropAt(ctx, props, 'cooler', ws.x - 16, footY)
}
const PROP_DROP = 8

function drawWorkstation(
  ctx: CanvasRenderingContext2D,
  action: Action,
  ws: { x: number; y: number; w: number; h: number },
  accent: string,
) {
  contactShadow(ctx, ws, action === 'robot' || action === 'celebrating')
  // Scale the authored (60x34) prop up to fill the real workstation footprint.
  ctx.save()
  ctx.translate(Math.round(ws.x), Math.round(ws.y))
  ctx.scale(ws.w / WS_BASE_W, ws.h / WS_BASE_H)

  const w = WS_BASE_W
  const h = WS_BASE_H
  const px = 0
  const py = 0
  const shadow = 'rgba(0,0,0,0.28)'

  // Ground shadow under every station.
  ctx.fillStyle = shadow
  ctx.fillRect(px + 3, py + h - 3, w - 6, 5)

  switch (action) {
    case 'hammering': {
      // Anvil: dark body on a wooden block.
      ctx.fillStyle = '#78350f'; ctx.fillRect(px + w / 2 - 14, py + h - 12, 28, 12) // block
      ctx.fillStyle = '#334155'; ctx.fillRect(px + w / 2 - 18, py + h - 20, 36, 8) // face
      ctx.fillStyle = '#1e293b'; ctx.fillRect(px + w / 2 - 8, py + h - 26, 22, 8) // horn
      ctx.fillStyle = '#475569'; ctx.fillRect(px + w / 2 - 18, py + h - 20, 36, 2) // top hi
      break
    }
    case 'testing': {
      // Lab bench with racked flasks.
      ctx.fillStyle = '#1f2937'; ctx.fillRect(px, py + h - 16, w, 16) // bench
      ctx.fillStyle = '#0f172a'; ctx.fillRect(px, py + h - 16, w, 3)
      ctx.fillStyle = '#2dd4bf'; ctx.fillRect(px + 10, py + h - 26, 6, 10) // flask 1
      ctx.fillStyle = '#f472b6'; ctx.fillRect(px + 24, py + h - 24, 6, 8) // flask 2
      ctx.fillStyle = '#fbbf24'; ctx.fillRect(px + 38, py + h - 28, 6, 12) // flask 3
      break
    }
    case 'robot': {
      // Charging/server rack the robot docks at.
      ctx.fillStyle = '#1e1b4b'; ctx.fillRect(px + w / 2 - 16, py, 32, h) // tower
      ctx.fillStyle = '#4f46e5'; ctx.fillRect(px + w / 2 - 12, py + 6, 24, 4)
      ctx.fillStyle = '#a5b4fc'; ctx.fillRect(px + w / 2 - 12, py + 14, 24, 3)
      ctx.fillStyle = '#818cf8'; ctx.fillRect(px + w / 2 - 12, py + 22, 16, 3)
      break
    }
    case 'celebrating': {
      // Podium / finished-goods pallet with a trophy.
      ctx.fillStyle = '#334155'; ctx.fillRect(px + w / 2 - 16, py + h - 14, 32, 14) // podium
      ctx.fillStyle = '#475569'; ctx.fillRect(px + w / 2 - 16, py + h - 14, 32, 3)
      ctx.fillStyle = '#fbbf24'; ctx.fillRect(px + w / 2 - 4, py + h - 26, 8, 12) // trophy cup
      ctx.fillStyle = '#f59e0b'; ctx.fillRect(px + w / 2 - 6, py + h - 16, 12, 2) // base
      break
    }
    default: {
      // Desk (drawing / inspecting / approving / idle): a lighter maple table
      // (so it pops on wood floors) with a dark outline, a paper, and a
      // monitor tinted to the station accent.
      ctx.fillStyle = '#1c1207'; ctx.fillRect(px - 1, py + h - 15, w + 2, 16) // outline
      ctx.fillStyle = '#a9885a'; ctx.fillRect(px, py + h - 14, w, 14) // desk top (maple)
      ctx.fillStyle = '#c9a878'; ctx.fillRect(px, py + h - 14, w, 3) // top highlight
      ctx.fillStyle = '#8a6c44'; ctx.fillRect(px, py + h - 4, w, 4) // front-edge shadow
      ctx.fillStyle = '#f8fafc'; ctx.fillRect(px + 8, py + h - 12, 14, 10) // paper
      ctx.fillStyle = '#cbd5e1'; ctx.fillRect(px + 8, py + h - 12, 14, 1)
      ctx.fillStyle = '#1e293b'; ctx.fillRect(px + w - 27, py + h - 29, 22, 18) // monitor bezel
      ctx.fillStyle = accent; ctx.fillRect(px + w - 25, py + h - 27, 18, 12) // screen
      ctx.fillStyle = 'rgba(255,255,255,0.3)'; ctx.fillRect(px + w - 25, py + h - 27, 18, 2)
      break
    }
  }
  ctx.restore()
}

// Blit one composed frame at (px, py) top-left, ART_PX base units per art
// pixel (= `zoom` CSS px after the scene scale), optionally mirrored
// horizontally. `bob` (base units) lifts the body but not its ground shadow.
// One filled rect per opaque pixel.
function drawSprite(ctx: CanvasRenderingContext2D, f: Frame, px: number, py: number, bob: number, facing: 1 | -1, squash = false) {
  // The shadow stays on the ground and shrinks as the body lifts.
  drawGroundShadow(ctx, px + SPRITE_W / 2, py + SPRITE_H, Math.round(-bob / ART_PX))

  ctx.save()
  if (squash) {
    const fx = px + SPRITE_W / 2
    const fy = py + SPRITE_H
    ctx.translate(fx, fy)
    ctx.scale((SPRITE_W + ART_PX) / SPRITE_W, (SPRITE_H - ART_PX) / SPRITE_H)
    ctx.translate(-fx, -fy)
  }
  if (facing === -1) {
    ctx.translate(px + SPRITE_W, py + bob)
    ctx.scale(-1, 1)
  } else {
    ctx.translate(px, py + bob)
  }
  for (let y = 0; y < f.rows.length; y++) {
    const row = f.rows[y]
    for (let x = 0; x < row.length; x++) {
      const color = f.palette[row[x]]
      if (!color) continue
      ctx.fillStyle = color
      ctx.fillRect(x * ART_PX, y * ART_PX, ART_PX, ART_PX)
    }
  }
  ctx.restore()
}

// A tiny pixel speech bubble: dark 5x7 chip with a one-pixel tail and a glyph
// from GLYPHS. `y` is the chip's top; `cell` the size of one bubble pixel
// (half size for the pop-in). Rects only, no allocation.
function drawEmote(ctx: CanvasRenderingContext2D, kind: number, cx: number, y: number, cell: number, color: string) {
  const x0 = Math.round(cx / ART_PX) * ART_PX - 2 * cell
  ctx.fillStyle = 'rgba(15,23,42,0.85)'
  ctx.fillRect(x0, y, 5 * cell, 7 * cell)
  ctx.fillRect(x0 + 2 * cell, y + 7 * cell, cell, cell)
  const rows = GLYPHS[kind]
  const wide = kind === 4
  const w = wide ? 5 : 3
  const gx = x0 + (wide ? 0 : cell)
  const gy = y + (wide ? 2 : 1) * cell
  const main = kind === 1 ? color : kind === 2 ? '#f1f5f9' : kind === 3 ? '#fde047' : '#4ade80'
  for (let r = 0; r < rows.length; r++) {
    ctx.fillStyle = kind === 3 && r >= 3 ? '#94a3b8' : main
    for (let c = 0; c < w; c++) {
      if (rows[r] & (1 << (w - 1 - c))) ctx.fillRect(gx + c * cell, gy + r * cell, cell, cell)
    }
  }
}

function defaultCanvas(w: number, h: number): CanvasLike | null {
  if (typeof OffscreenCanvas !== 'undefined') return new OffscreenCanvas(w, h) as unknown as CanvasLike
  if (typeof document !== 'undefined') {
    const c = document.createElement('canvas')
    c.width = w
    c.height = h
    return c
  }
  return null
}
