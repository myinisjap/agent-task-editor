// Imperative office-floor sim for the dashboard visualization. Holds sprite
// entities and station layout OUTSIDE React (like pixel-agents' OfficeState),
// stepped by a requestAnimationFrame loop in TaskFactory.tsx and drawn onto a
// Canvas 2D context with integer-zoom pixel blitting. No React, no DOM here.
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
// sprite's grid box); the walk target is a random point within the owning
// station's patch. Sprites walk to target, pause + act, then pick a new one.
type Sprite = {
  stationIndex: number
  x: number
  y: number
  tx: number
  ty: number
  speed: number // px/sec
  facing: 1 | -1
  moving: boolean
  pause: number // seconds left to stay parked before next walk
  frameTimer: number // seconds accumulated in current action-frame
  frameDur: number // seconds per action-frame (per-sprite jitter)
  frame: number
  legTimer: number
  leg: 0 | 1
  variant: Variant // per-worker recolor so a crew isn't clones
}

// Layout constants (CSS px). ZOOM is the integer pixel-art scale (3-4x → we
// use 4 for a roomy floor). Sprite box is GRID_W*ZOOM by GRID_H*ZOOM.
const ZOOM = 3
const SPRITE_W = GRID_W * ZOOM // 48
const SPRITE_H = GRID_H * ZOOM // 72
const CAP = 6 // max sprites drawn per station
const STATION_W = 150
const STATION_GAP = 12
const PAD = 16
const HEADER_H = 20 // label text row at top of each station patch
const FLOOR_H = 220 // patch height sprites roam within
const FOOTER_H = 34 // count + name row at bottom

export const SCENE_H = PAD + HEADER_H + FLOOR_H + FOOTER_H + PAD

export class OfficeScene {
  stations: Station[] = []
  private sprites: Sprite[] = []
  private aisle = 0 // scrolling floor-line phase

  setStations(stations: Station[]) {
    this.stations = stations
    this.rebuildSprites()
  }

  width(): number {
    return PAD * 2 + this.stations.length * STATION_W + Math.max(0, this.stations.length - 1) * STATION_GAP
  }

  private patch(i: number): { x: number; y: number; w: number; h: number } {
    return {
      x: PAD + i * (STATION_W + STATION_GAP),
      y: PAD + HEADER_H,
      w: STATION_W,
      h: FLOOR_H,
    }
  }

  // Roamable rect for a sprite (patch inset so the whole sprite box stays
  // inside the patch).
  private roamRect(i: number) {
    const p = this.patch(i)
    return {
      minX: p.x + 6,
      maxX: p.x + p.w - SPRITE_W - 6,
      minY: p.y + 8,
      maxY: p.y + p.h - SPRITE_H - 8,
    }
  }

  private randTarget(i: number): { x: number; y: number } {
    const r = this.roamRect(i)
    const x = r.maxX > r.minX ? r.minX + Math.random() * (r.maxX - r.minX) : r.minX
    const y = r.maxY > r.minY ? r.minY + Math.random() * (r.maxY - r.minY) : r.minY
    return { x, y }
  }

  private rebuildSprites() {
    this.sprites = []
    this.stations.forEach((st, i) => {
      const n = Math.min(st.count, CAP)
      for (let k = 0; k < n; k++) {
        const start = this.randTarget(i)
        const t = this.randTarget(i)
        this.sprites.push({
          stationIndex: i,
          x: start.x,
          y: start.y,
          tx: t.x,
          ty: t.y,
          speed: 14 + Math.random() * 14,
          facing: 1,
          moving: true,
          pause: 0,
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

  step(dt: number) {
    this.aisle = (this.aisle + dt * 18) % 12
    for (const s of this.sprites) {
      const st = this.stations[s.stationIndex]
      if (!st) continue

      // Advance action-frame animation always (breathing/acting continues
      // while parked; a robot keeps scanning while it rolls).
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
          s.pause = 0.8 + Math.random() * 2.2 // stand and act
        } else {
          s.x += (dx / dist) * move
          s.y += (dy / dist) * move
          if (Math.abs(dx) > 0.5) s.facing = dx < 0 ? -1 : 1
          // Walk-cycle leg alternation.
          s.legTimer += dt
          if (s.legTimer >= 0.16) {
            s.legTimer -= 0.16
            s.leg = s.leg === 0 ? 1 : 0
          }
        }
      } else {
        s.pause -= dt
        if (s.pause <= 0) {
          const t = this.randTarget(s.stationIndex)
          s.tx = t.x
          s.ty = t.y
          s.moving = true
        }
      }
    }
  }

  // Render one static frame (used for prefers-reduced-motion): no motion, one
  // pose each. Same draw path as the animated version.
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

      // Station patch: faint fill + colored top accent, dashed scrolling aisle.
      ctx.fillStyle = 'rgba(30,41,59,0.35)'
      ctx.fillRect(p.x, p.y, p.w, p.h)
      ctx.fillStyle = st.color
      ctx.fillRect(p.x, p.y, p.w, 3)

      ctx.save()
      ctx.globalAlpha = 0.4
      ctx.strokeStyle = st.color
      ctx.setLineDash([6, 6])
      ctx.lineDashOffset = -this.aisle
      ctx.beginPath()
      ctx.moveTo(p.x, p.y + p.h / 2)
      ctx.lineTo(p.x + p.w, p.y + p.h / 2)
      ctx.stroke()
      ctx.restore()

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
        ctx.fillText(`+${overflow}`, p.x + p.w - 6, p.y + 10)
        ctx.textAlign = 'center'
      }
    })

    // Draw sprites sorted by y so lower ones overlap upper ones (depth).
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

// Blit one composed frame at (px, py) top-left, scaled ZOOM×, optionally
// mirrored horizontally (facing left). One filled rect per opaque pixel.
function drawSprite(ctx: CanvasRenderingContext2D, f: Frame, px: number, py: number, facing: 1 | -1) {
  // Soft contact shadow so sprites sit on the floor rather than float.
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
