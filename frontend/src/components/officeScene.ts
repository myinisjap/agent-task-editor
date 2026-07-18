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
  GRID_W,
  GRID_H,
  type Action,
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
  leg: 0 | 1
  variant: Variant
}

// Layout constants (CSS px). ZOOM is the integer pixel-art scale.
const ZOOM = 3
const SPRITE_W = GRID_W * ZOOM // 48
const SPRITE_H = GRID_H * ZOOM // 72
const CAP = 6 // max sprites drawn per station
const STATION_W = 168
const STATION_GAP = 12
const PAD = 16
const HEADER_H = 20 // label row above each patch
const FLOOR_H = 176 // patch height sprites roam within
const FOOTER_H = 34 // count + name row below each patch
const CELL_H = HEADER_H + FLOOR_H + FOOTER_H // full vertical footprint of one station

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
    return PAD * 2 + rows * CELL_H + (rows - 1) * STATION_GAP
  }

  private patch(i: number): Patch {
    const col = i % this.cols
    const row = Math.floor(i / this.cols)
    return {
      x: PAD + col * (STATION_W + STATION_GAP),
      y: PAD + row * (CELL_H + STATION_GAP) + HEADER_H,
      w: STATION_W,
      h: FLOOR_H,
    }
  }

  // The workstation footprint: a block centered along the patch's back wall
  // (upper area). Sprites work at a slot just below its front edge.
  private workstation(i: number): { x: number; y: number; w: number; h: number } {
    const p = this.patch(i)
    const w = 60
    const h = 34
    return { x: p.x + p.w / 2 - w / 2, y: p.y + 14, w, h }
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
          // Arrived. If this was a bid for the work slot, stay a while and act
          // facing the station; otherwise idle briefly then try to queue.
          s.pause = s.atWork ? 2.5 + Math.random() * 3 : 0.6 + Math.random() * 1.8
          if (s.atWork) s.facing = 1 // face up/into the workstation
        } else {
          s.x += (dx / dist) * move
          s.y += (dy / dist) * move
          if (Math.abs(dx) > 0.5) s.facing = dx < 0 ? -1 : 1
          s.legTimer += dt
          if (s.legTimer >= 0.16) {
            s.legTimer -= 0.16
            s.leg = s.leg === 0 ? 1 : 0
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

    // Dark backdrop between rooms (reads as building shell / hallways).
    ctx.fillStyle = '#161a24'
    ctx.fillRect(0, 0, cssW, cssH)

    // Each station is a furnished ROOM: warm textured floor, a back wall with
    // wall-mounted décor, scatter props, then the workstation. Sprites are
    // drawn afterwards (depth-sorted) so they walk in front of the floor/props
    // but behind nothing that should occlude them.
    this.stations.forEach((st, i) => {
      const p = this.patch(i)
      const theme = ROOM_THEME[st.action] ?? ROOM_THEME.idle
      const rng = mulberry32(0x9e37 + i * 2654435761)

      drawFloor(ctx, p, theme)
      drawBackWall(ctx, p, theme, i)

      // Accent strip under the label so the stage color still reads.
      ctx.fillStyle = st.color
      ctx.fillRect(p.x, p.y, p.w, 3)

      // Behind-props: scatter along the back half so sprites pass in front.
      drawScatter(ctx, p, rng, 'back')

      drawWorkstation(ctx, st.action, this.workstation(i), st.color)

      // Header label.
      ctx.fillStyle = '#cbd5e1'
      ctx.font = '600 11px ui-sans-serif, system-ui, sans-serif'
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText(st.name.toUpperCase(), p.x + p.w / 2, p.y - HEADER_H / 2)

      // Count.
      ctx.fillStyle = '#e2e8f0'
      ctx.font = '600 18px ui-sans-serif, system-ui, sans-serif'
      ctx.fillText(String(st.count), p.x + p.w / 2, p.y + p.h + FOOTER_H / 2)

      const overflow = st.count - CAP
      if (overflow > 0) {
        ctx.fillStyle = '#94a3b8'
        ctx.font = '10px ui-sans-serif, system-ui, sans-serif'
        ctx.textAlign = 'right'
        ctx.fillText(`+${overflow}`, p.x + p.w - 6, p.y + 12)
        ctx.textAlign = 'center'
      }
    })

    // Depth-sort sprites back-to-front so lower ones overlap upper ones.
    const ordered = [...this.sprites].sort((a, b) => a.y - b.y)
    for (const s of ordered) {
      const st = this.stations[s.stationIndex]
      if (!st) continue
      const walkLeg = s.moving && st.action !== 'robot' ? s.leg : -1
      const f = composeFrame(st.action, s.frame, walkLeg, s.variant)
      drawSprite(ctx, f, s.x, s.y, s.facing)
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

// Warm, per-room palette + floor style. Grouped so build stages get wood,
// review/QA get tile, done gets a cozy carpet.
type RoomTheme = {
  floor: string
  floorAlt: string // plank/tile line color
  wall: string
  wallTrim: string
  style: 'wood' | 'tile' | 'carpet'
}
const ROOM_THEME: Record<Action, RoomTheme> = {
  idle:        { floor: '#6b4f34', floorAlt: '#5c432c', wall: '#463022', wallTrim: '#5c4230', style: 'wood' },
  drawing:     { floor: '#6b4f34', floorAlt: '#5c432c', wall: '#463022', wallTrim: '#5c4230', style: 'wood' },
  inspecting:  { floor: '#7a5a3a', floorAlt: '#694c30', wall: '#4a3222', wallTrim: '#60422c', style: 'wood' },
  hammering:   { floor: '#6b4f34', floorAlt: '#5c432c', wall: '#463022', wallTrim: '#5c4230', style: 'wood' },
  testing:     { floor: '#c9c1b0', floorAlt: '#b8af9c', wall: '#5a5347', wallTrim: '#726a59', style: 'tile' },
  robot:       { floor: '#3a3550', floorAlt: '#322e46', wall: '#2a2740', wallTrim: '#413c5e', style: 'tile' },
  approving:   { floor: '#c9c1b0', floorAlt: '#b8af9c', wall: '#5a5347', wallTrim: '#726a59', style: 'tile' },
  celebrating: { floor: '#7c4a52', floorAlt: '#6d4048', wall: '#4a2e33', wallTrim: '#5f3a41', style: 'carpet' },
  waving:      { floor: '#c9c1b0', floorAlt: '#b8af9c', wall: '#5a5347', wallTrim: '#726a59', style: 'tile' },
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

// Textured warm floor for one room.
function drawFloor(ctx: CanvasRenderingContext2D, p: Patch, t: RoomTheme) {
  ctx.fillStyle = t.floor
  ctx.fillRect(p.x, p.y, p.w, p.h)
  ctx.fillStyle = t.floorAlt
  if (t.style === 'wood') {
    // Horizontal planks.
    for (let y = p.y + 12; y < p.y + p.h; y += 14) ctx.fillRect(p.x, y, p.w, 1)
  } else if (t.style === 'tile') {
    // Grid grout.
    for (let x = p.x + 16; x < p.x + p.w; x += 16) ctx.fillRect(x, p.y, 1, p.h)
    for (let y = p.y + 16; y < p.y + p.h; y += 16) ctx.fillRect(p.x, y, p.w, 1)
  } else {
    // Carpet speckle.
    const rng = mulberry32(p.x * 131 + p.y)
    for (let k = 0; k < 40; k++) {
      ctx.globalAlpha = 0.25
      ctx.fillRect(p.x + rng() * p.w, p.y + rng() * p.h, 1, 1)
    }
    ctx.globalAlpha = 1
  }
}

// Back wall band + one wall-mounted décor item (bookshelf / picture / clock),
// chosen by station index so rooms differ but stay stable.
function drawBackWall(ctx: CanvasRenderingContext2D, p: Patch, t: RoomTheme, i: number) {
  const wallH = 12
  ctx.fillStyle = t.wall
  ctx.fillRect(p.x, p.y, p.w, wallH)
  ctx.fillStyle = t.wallTrim
  ctx.fillRect(p.x, p.y + wallH, p.w, 2) // baseboard trim

  const kind = i % 3
  const cx = p.x + p.w / 2
  if (kind === 0) {
    // Bookshelf with colored spines.
    const bx = cx - 26
    ctx.fillStyle = '#5c4128'; ctx.fillRect(bx, p.y + 2, 52, 8)
    const spines = ['#dc2626', '#16a34a', '#2563eb', '#f59e0b', '#7c3aed', '#e11d48']
    for (let k = 0; k < 12; k++) {
      ctx.fillStyle = spines[(i + k) % spines.length]
      ctx.fillRect(bx + 2 + k * 4, p.y + 3, 3, 6)
    }
  } else if (kind === 1) {
    // Framed picture: sky over ground.
    ctx.fillStyle = '#8b6b45'; ctx.fillRect(cx - 12, p.y + 1, 24, 10)
    ctx.fillStyle = '#7dd3fc'; ctx.fillRect(cx - 10, p.y + 2, 20, 5)
    ctx.fillStyle = '#4ade80'; ctx.fillRect(cx - 10, p.y + 7, 20, 2)
  } else {
    // Wall clock.
    ctx.fillStyle = '#e2e8f0'; ctx.fillRect(cx - 5, p.y + 1, 10, 10)
    ctx.fillStyle = '#1e293b'; ctx.fillRect(cx - 1, p.y + 2, 2, 5); ctx.fillRect(cx, p.y + 5, 3, 2)
  }
}

// Scatter a stable set of small props (plants, boxes, mugs) in a band of the
// patch. 'back' fills the upper third (behind sprites); 'front' the bottom
// edge (in front of sprites).
function drawScatter(ctx: CanvasRenderingContext2D, p: Patch, rng: () => number, band: 'back' | 'front') {
  const n = band === 'back' ? 2 + Math.floor(rng() * 2) : 1 + Math.floor(rng() * 2)
  const yMin = band === 'back' ? p.y + 16 : p.y + p.h - 14
  const ySpan = band === 'back' ? p.h * 0.22 : 10
  for (let k = 0; k < n; k++) {
    const x = p.x + 4 + rng() * (p.w - 20)
    const y = yMin + rng() * ySpan
    const pick = rng()
    if (pick < 0.55) drawPlant(ctx, x, y)
    else if (pick < 0.8) drawBox(ctx, x, y)
    else drawMug(ctx, x, y)
  }
}

function propShadow(ctx: CanvasRenderingContext2D, x: number, y: number, w: number) {
  ctx.fillStyle = 'rgba(0,0,0,0.25)'
  ctx.fillRect(x - 1, y, w + 2, 2)
}

function drawPlant(ctx: CanvasRenderingContext2D, x: number, y: number) {
  propShadow(ctx, x, y + 12, 8)
  ctx.fillStyle = '#7c4a2a'; ctx.fillRect(x + 1, y + 8, 6, 5) // pot
  ctx.fillStyle = '#8a5433'; ctx.fillRect(x + 1, y + 8, 6, 1)
  ctx.fillStyle = '#22c55e'; ctx.fillRect(x, y + 2, 3, 7); ctx.fillRect(x + 5, y + 3, 3, 6) // fronds
  ctx.fillStyle = '#16a34a'; ctx.fillRect(x + 2, y, 4, 6)
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
function drawWorkstation(
  ctx: CanvasRenderingContext2D,
  action: Action,
  ws: { x: number; y: number; w: number; h: number },
  accent: string,
) {
  const { x, y, w, h } = ws
  const px = Math.round(x)
  const py = Math.round(y)
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
