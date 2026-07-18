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

    // Floor grid backdrop.
    ctx.fillStyle = '#0f172a'
    ctx.fillRect(0, 0, cssW, cssH)
    ctx.strokeStyle = 'rgba(148,163,184,0.06)'
    ctx.lineWidth = 1
    for (let gx = 0; gx <= cssW; gx += 16) {
      ctx.beginPath(); ctx.moveTo(gx + 0.5, 0); ctx.lineTo(gx + 0.5, cssH); ctx.stroke()
    }
    for (let gy = 0; gy <= cssH; gy += 16) {
      ctx.beginPath(); ctx.moveTo(0, gy + 0.5); ctx.lineTo(cssW, gy + 0.5); ctx.stroke()
    }

    this.stations.forEach((st, i) => {
      const p = this.patch(i)

      ctx.fillStyle = 'rgba(30,41,59,0.35)'
      ctx.fillRect(p.x, p.y, p.w, p.h)
      ctx.fillStyle = st.color
      ctx.fillRect(p.x, p.y, p.w, 3)

      // Header label.
      ctx.fillStyle = '#94a3b8'
      ctx.font = '600 11px ui-sans-serif, system-ui, sans-serif'
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText(st.name.toUpperCase(), p.x + p.w / 2, p.y - HEADER_H / 2)

      // Count.
      ctx.fillStyle = '#e2e8f0'
      ctx.font = '600 18px ui-sans-serif, system-ui, sans-serif'
      ctx.fillText(String(st.count), p.x + p.w / 2, p.y + p.h + FOOTER_H / 2)

      // Overflow badge.
      const overflow = st.count - CAP
      if (overflow > 0) {
        ctx.fillStyle = '#64748b'
        ctx.font = '10px ui-sans-serif, system-ui, sans-serif'
        ctx.textAlign = 'right'
        ctx.fillText(`+${overflow}`, p.x + p.w - 6, p.y + 12)
        ctx.textAlign = 'center'
      }

      drawWorkstation(ctx, st.action, this.workstation(i), st.color)
    })

    // Depth-sort: draw sprites back-to-front so lower ones overlap upper ones.
    const ordered = [...this.sprites].sort((a, b) => a.y - b.y)
    for (const s of ordered) {
      const st = this.stations[s.stationIndex]
      if (!st) continue
      const walkLeg = s.moving && st.action !== 'robot' ? s.leg : -1
      const f = composeFrame(st.action, s.frame, walkLeg, s.variant)
      drawSprite(ctx, f, s.x, s.y, s.facing)
    }
  }
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
      // Desk (drawing / inspecting / approving / idle): a table with a paper
      // and a colored monitor/sign tinted to the station accent.
      ctx.fillStyle = '#3f2d1d'; ctx.fillRect(px, py + h - 14, w, 14) // desk top
      ctx.fillStyle = '#2a1d13'; ctx.fillRect(px, py + h - 14, w, 3)
      ctx.fillStyle = '#e2e8f0'; ctx.fillRect(px + 8, py + h - 12, 14, 10) // paper
      ctx.fillStyle = accent; ctx.fillRect(px + w - 26, py + h - 28, 20, 16) // monitor/sign
      ctx.fillStyle = 'rgba(255,255,255,0.25)'; ctx.fillRect(px + w - 26, py + h - 28, 20, 3)
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
