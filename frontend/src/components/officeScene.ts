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
import {
  composeFrame,
  actionFrameCount,
  VARIANTS,
  WALK_FRAME_COUNT,
  GRID_W,
  GRID_H,
  type Action,
  type Dir,
  type Frame,
  type Variant,
} from './pixelSprites'

export type Station = {
  key: string
  name: string
  color: string
  action: Action
  count: number
}

// One character on the floor. Positions are in CSS pixels (top-left of the
// sprite's grid box). A sprite is either walking to its target, or parked
// (acting). If it holds the work slot it acts facing the workstation, then
// yields the slot and wanders to an idle spot before queueing again.
type Sprite = {
  stationIndex: number
  x: number
  y: number
  tx: number
  ty: number
  speed: number // px/sec
  facing: 1 | -1
  moving: boolean
  atWork: boolean // currently occupying this station's work slot
  pause: number // seconds left parked before next move
  frameTimer: number
  frameDur: number
  frame: number
  legTimer: number
  leg: number // walk-cycle index (0..WALK_FRAME_COUNT-1)
  dir: Dir // facing: front (toward viewer) / side / back (away/at station)
  variant: Variant
}

// Layout constants (CSS px). ZOOM is the integer pixel-art scale.
const ZOOM = 3
const SPRITE_W = GRID_W * ZOOM // 48
const SPRITE_H = GRID_H * ZOOM // 72
const CAP = 6 // max sprites drawn per station
const STATION_W = 168
const STATION_GAP = 0 // no gap: adjacent stations abut into one continuous scene
const PAD = 16
// Stations sit flush on one shared floor; the label + count live on the
// workstation sign, so there's no header/footer text band around each cell.
const SIGN_H = 30 // room above the desk reserved for the name/count plaque
const FLOOR_H = 190 // patch height sprites roam within
const CELL_H = SIGN_H + FLOOR_H // full vertical footprint of one station

type Patch = { x: number; y: number; w: number; h: number }

export class OfficeScene {
  stations: Station[] = []
  private sprites: Sprite[] = []
  private aisle = 0
  private cols = 1
  private layoutW = STATION_W + PAD * 2

  setStations(stations: Station[]) {
    this.stations = stations
    this.relayout()
    this.rebuildSprites()
  }

  // Choose column count from available width, then remember the resulting
  // canvas size. Called on station change and on container resize. When the
  // column count changes, station patches move to new grid cells, so any
  // existing sprites must be re-snapped into their (new) patch or they'd be
  // left stranded outside it (invisible).
  layout(availWidth: number) {
    const usable = Math.max(STATION_W, availWidth - PAD * 2)
    const fit = Math.floor((usable + STATION_GAP) / (STATION_W + STATION_GAP))
    const cols = Math.max(1, Math.min(this.stations.length || 1, fit))
    const changed = cols !== this.cols
    this.cols = cols
    this.relayout()
    if (changed) this.replaceSprites()
  }

  // Drop every sprite onto a fresh idle spot inside its current patch. Used
  // after a column-count change so nobody is left in a stale grid cell.
  private replaceSprites() {
    for (const s of this.sprites) {
      const spot = this.idleTarget(s.stationIndex)
      s.x = spot.x
      s.y = spot.y
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

  width(): number {
    return this.layoutW
  }

  height(): number {
    const rows = Math.max(1, Math.ceil((this.stations.length || 1) / Math.max(1, this.cols)))
    return PAD * 2 + rows * CELL_H
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

  // A random idle spot in the lower part of the patch (out of the way of the
  // workstation), where sprites wait between turns.
  private idleTarget(i: number): { x: number; y: number } {
    const p = this.patch(i)
    const minX = p.x + 4
    const maxX = p.x + p.w - SPRITE_W - 4
    // Lower ~half of the patch, below the workstation, but a generous band so
    // a crew spreads out instead of stacking on one line.
    const minY = p.y + p.h * 0.42
    const maxY = p.y + p.h - SPRITE_H
    return {
      x: maxX > minX ? minX + Math.random() * (maxX - minX) : minX,
      y: maxY > minY ? minY + Math.random() * (maxY - minY) : minY,
    }
  }

  private rebuildSprites() {
    this.sprites = []
    this.stations.forEach((st, i) => {
      const n = Math.min(st.count, CAP)
      for (let k = 0; k < n; k++) {
        const start = this.idleTarget(i)
        const t = this.idleTarget(i)
        this.sprites.push({
          stationIndex: i,
          x: start.x,
          y: start.y,
          tx: t.x,
          ty: t.y,
          speed: 16 + Math.random() * 14,
          facing: 1,
          moving: true,
          atWork: false,
          pause: Math.random() * 1.5,
          frameTimer: Math.random(),
          frameDur: 0.22 + Math.random() * 0.12,
          frame: Math.floor(Math.random() * 4),
          legTimer: 0,
          leg: 0,
          dir: 'front',
          variant: VARIANTS[Math.floor(Math.random() * VARIANTS.length)],
        })
      }
    })
  }

  // True if any sprite currently owns station i's work slot.
  private slotTaken(stationIndex: number): boolean {
    return this.sprites.some((s) => s.stationIndex === stationIndex && s.atWork)
  }

  step(dt: number) {
    this.aisle = (this.aisle + dt * 18) % 12
    for (const s of this.sprites) {
      const st = this.stations[s.stationIndex]
      if (!st) continue

      s.frameTimer += dt
      if (s.frameTimer >= s.frameDur) {
        s.frameTimer -= s.frameDur
        s.frame = (s.frame + 1) % actionFrameCount(st.action)
      }

      if (s.moving) {
        const dx = s.tx - s.x
        const dy = s.ty - s.y
        const dist = Math.hypot(dx, dy)
        const move = s.speed * dt
        if (dist <= move || dist === 0) {
          s.x = s.tx
          s.y = s.ty
          s.moving = false
          // Arrived. At the work slot: face the station (back to viewer) and
          // stay a while to act; otherwise idle facing the viewer (front).
          s.pause = s.atWork ? 2.5 + Math.random() * 3 : 0.6 + Math.random() * 1.8
          s.dir = s.atWork ? 'back' : 'front'
          if (s.atWork) s.facing = 1
        } else {
          s.x += (dx / dist) * move
          s.y += (dy / dist) * move
          // Pick facing from the dominant travel axis: mostly-horizontal → side
          // profile (mirror via facing); mostly-vertical → back if walking up,
          // front if walking down.
          if (Math.abs(dx) >= Math.abs(dy)) {
            s.dir = 'side'
            if (Math.abs(dx) > 0.5) s.facing = dx < 0 ? -1 : 1
          } else {
            s.dir = dy < 0 ? 'back' : 'front'
          }
          s.legTimer += dt
          if (s.legTimer >= 0.15) {
            s.legTimer -= 0.15
            s.leg = (s.leg + 1) % WALK_FRAME_COUNT
          }
        }
      } else {
        s.pause -= dt
        if (s.pause <= 0) {
          if (s.atWork) {
            // Done working: yield the slot and step aside to idle.
            s.atWork = false
            const t = this.idleTarget(s.stationIndex)
            s.tx = t.x
            s.ty = t.y
            s.moving = true
          } else if (!this.slotTaken(s.stationIndex)) {
            // Slot free: claim it and walk up to the workstation.
            s.atWork = true
            const slot = this.workSlot(s.stationIndex)
            s.tx = slot.x
            s.ty = slot.y
            s.moving = true
          } else {
            // Slot busy: mill around and re-check next pause.
            const t = this.idleTarget(s.stationIndex)
            s.tx = t.x
            s.ty = t.y
            s.moving = true
          }
        }
      }
    }
  }

  draw(ctx: CanvasRenderingContext2D, cssW: number, cssH: number) {
    ctx.imageSmoothingEnabled = false
    ctx.clearRect(0, 0, cssW, cssH)

    // ONE continuous warm wood floor across the whole scene. Stations abut with
    // no gap, so with a single floor the whole thing reads as one open room
    // rather than a grid of boxes.
    drawSharedFloor(ctx, cssW, cssH)

    // Short back-props (behind the desk), then workstations + signs.
    this.stations.forEach((st, i) => {
      const p = this.patch(i)
      const rng = mulberry32(0x9e37 + i * 2654435761)
      drawScatter(ctx, p, rng, 'back', 'noLamp')
      drawWorkstation(ctx, st.action, this.workstation(i), st.color)
      drawSign(ctx, this.workstation(i), st)
    })

    // Tall floor lamps AFTER workstations (so a lamp beside a desk isn't
    // clipped by it) but before sprites (so workers still pass in front). Same
    // rng seed → identical positions as the back pass above.
    this.stations.forEach((_st, i) => {
      const p = this.patch(i)
      const rng = mulberry32(0x9e37 + i * 2654435761)
      drawScatter(ctx, p, rng, 'back', 'lampOnly')
    })

    // Depth-sort sprites back-to-front so lower ones overlap upper ones.
    const ordered = [...this.sprites].sort((a, b) => a.y - b.y)
    for (const s of ordered) {
      const st = this.stations[s.stationIndex]
      if (!st) continue
      const walkLeg = s.moving && st.action !== 'robot' ? s.leg : -1
      const f = composeFrame(st.action, s.frame, walkLeg, s.variant, s.dir)
      // 'side' is authored facing right; mirror for leftward travel. Front and
      // back are symmetric, so only side needs the facing flip.
      const flip = s.dir === 'side' && s.facing === -1
      drawSprite(ctx, f, s.x, s.y, flip ? -1 : 1)
    }

    // Front-props: near the bottom edge, drawn last so they overlap sprites
    // (a plant a worker walks behind), grounding the room.
    this.stations.forEach((_st, i) => {
      const p = this.patch(i)
      const rng = mulberry32(0x51ed + i * 2654435761)
      drawScatter(ctx, p, rng, 'front')
    })
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

// Scatter a stable set of small props (plants, boxes, mugs, lamps) in a band of
// the patch. 'back' fills the upper third; 'front' the bottom edge.
//
// The `only` phase controls which props render, WITHOUT changing the rng draw
// sequence — so prop positions/types stay identical across phases:
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
  const n = band === 'back' ? 2 + Math.floor(rng() * 2) : 1 + Math.floor(rng() * 2)
  const yMin = band === 'back' ? p.y + 16 : p.y + p.h - 14
  const ySpan = band === 'back' ? p.h * 0.22 : 10
  for (let k = 0; k < n; k++) {
    const x = p.x + 4 + rng() * (p.w - 20)
    const y = yMin + rng() * ySpan
    const pick = rng()
    // The floor lamp is tall (~character height); it only fits in the back band
    // (full patch height below it), so in the front band that slot is a plant.
    const isLamp = pick >= 0.45 && pick < 0.65 && band === 'back'
    if (isLamp) {
      if (only !== 'noLamp') drawLamp(ctx, x, y)
      continue
    }
    if (only === 'lampOnly') continue // this pass renders lamps only
    if (pick < 0.45) drawPlant(ctx, x, y)
    else if (pick < 0.65) drawPlant(ctx, x, y) // front-band lamp slot → plant
    else if (pick < 0.85) drawBox(ctx, x, y)
    else drawMug(ctx, x, y)
  }
}

function propShadow(ctx: CanvasRenderingContext2D, x: number, y: number, w: number) {
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

function drawWorkstation(
  ctx: CanvasRenderingContext2D,
  action: Action,
  ws: { x: number; y: number; w: number; h: number },
  accent: string,
) {
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

// Blit one composed frame at (px, py) top-left, scaled ZOOM×, optionally
// mirrored horizontally. One filled rect per opaque pixel.
function drawSprite(ctx: CanvasRenderingContext2D, f: Frame, px: number, py: number, facing: 1 | -1) {
  ctx.fillStyle = 'rgba(0,0,0,0.28)'
  ctx.fillRect(px + ZOOM * 2, py + SPRITE_H - ZOOM, SPRITE_W - ZOOM * 4, ZOOM)

  ctx.save()
  if (facing === -1) {
    ctx.translate(px + SPRITE_W, py)
    ctx.scale(-1, 1)
  } else {
    ctx.translate(px, py)
  }
  for (let y = 0; y < f.rows.length; y++) {
    const row = f.rows[y]
    for (let x = 0; x < row.length; x++) {
      const color = f.palette[row[x]]
      if (!color) continue
      ctx.fillStyle = color
      ctx.fillRect(x * ZOOM, y * ZOOM, ZOOM, ZOOM)
    }
  }
  ctx.restore()
}
