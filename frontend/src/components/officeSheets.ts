// PNG sprite sheets for the office scene: a higher-resolution worker and robot
// crew that replace the hand-typed character grids in pixelSprites.ts when
// they load. Nothing here is required: until the images decode (and in tests,
// where canvas/Image are stubs) officeScene keeps drawing the grid sprites.
//
// Sheets live in src/assets/office and are bundled by Vite, so the scene stays
// self-contained (no runtime fetch to an external service).
//
// Layout: fixed-size cells, one animation per row, frames left to right.
// Side-view rows face right (east); officeScene mirrors them for leftward
// travel. Feet (or the robot's thruster) sit on the cell's baseline.
import workerUrl from '../assets/office/worker.png'
import robotUrl from '../assets/office/robot.png'
import type { Action, Dir, Variant } from './pixelSprites'

export type SheetKind = 'worker' | 'robot'

export type SheetSpec = {
  cellW: number
  cellH: number
  // Row of the cell that the feet stand on, counted from the cell's top
  // (bottom edge of the lowest opaque pixel).
  baseline: number
  rows: Record<string, readonly [row: number, frames: number]>
}

export const WORKER_SHEET: SheetSpec = {
  cellW: 44,
  cellH: 48,
  baseline: 48,
  rows: {
    idle_south: [0, 1],
    idle_east: [1, 1],
    idle_north: [2, 1],
    drawing_north: [3, 6],
    drawing_east: [4, 6],
    drawing_south: [5, 6],
    hammering_north: [6, 6],
    hammering_east: [7, 6],
    hammering_south: [8, 6],
    inspecting_north: [9, 6],
    inspecting_east: [10, 6],
    inspecting_south: [11, 6],
    testing_north: [12, 6],
    testing_east: [13, 6],
    testing_south: [14, 6],
    approving_north: [15, 6],
    approving_east: [16, 6],
    approving_south: [17, 6],
    carrying_east: [18, 8],
    carrying_south: [19, 8],
    carrying_north: [20, 8],
    breath_south: [21, 4],
    walk_south: [22, 7],
    walk_east: [23, 7],
    walk_north: [24, 6],
    wave_south: [25, 4],
    celebrate_south: [26, 5],
  },
}

export const ROBOT_SHEET: SheetSpec = {
  cellW: 48,
  cellH: 48,
  baseline: 47,
  rows: {
    idle_still_south: [0, 1],
    idle_still_east: [1, 1],
    idle_still_north: [2, 1],
    idle_south: [3, 6],
    fly_south: [4, 6],
    fly_east: [5, 6],
    fly_north: [6, 6],
    working_north: [7, 6],
    carrying_east: [8, 6],
  },
}

export type Clip = { sheet: SheetKind; name: string; row: number; frames: number }

const COMPASS: Record<Dir, 'south' | 'east' | 'north'> = { front: 'south', side: 'east', back: 'north' }
const TOOLED = new Set<Action>(['drawing', 'hammering', 'inspecting', 'testing', 'approving'])

function clip(sheet: SheetKind, name: string): Clip {
  const spec = sheet === 'worker' ? WORKER_SHEET : ROBOT_SHEET
  const r = spec.rows[name]
  if (!r) throw new Error(`officeSheets: no ${sheet} row "${name}"`)
  return { sheet, name, row: r[0], frames: r[1] }
}

/**
 * Which sheet row to play for a sprite. Pure: officeScene calls it per sprite
 * and tests cover the mapping. `robots` is the all-robot crew toggle; the
 * 'robot' action (the agent-review station) always shows a robot.
 */
export function pickClip(o: { action: Action; dir: Dir; moving: boolean; carry: boolean; robots: boolean }): Clip {
  const d = COMPASS[o.dir]
  if (o.robots || o.action === 'robot') {
    if (o.moving) return clip('robot', o.carry && d === 'east' ? 'carrying_east' : `fly_${d}`)
    if (o.action === 'waving' || o.action === 'celebrating') return clip('robot', 'idle_south')
    if (d === 'north') return clip('robot', o.action === 'idle' ? 'idle_still_north' : 'working_north')
    if (d === 'east') return clip('robot', 'idle_still_east')
    return clip('robot', 'idle_south')
  }
  if (o.moving) return clip('worker', o.carry ? `carrying_${d}` : `walk_${d}`)
  if (o.action === 'waving') return clip('worker', 'wave_south')
  if (o.action === 'celebrating') return clip('worker', 'celebrate_south')
  if (TOOLED.has(o.action)) return clip('worker', `${o.action}_${d}`)
  return clip('worker', d === 'south' ? 'breath_south' : `idle_${d}`)
}

// ---- loading + per-variant recolour ----------------------------------------

type Canvasish = HTMLCanvasElement | OffscreenCanvas

export type Sheets = {
  worker: Canvasish
  robot: Canvasish
  // Worker sheet recoloured for VARIANTS[i]'s shirt, built on first use.
  workerFor(variant: Variant): Canvasish
}

function makeCanvas(w: number, h: number): Canvasish | null {
  try {
    if (typeof OffscreenCanvas !== 'undefined') return new OffscreenCanvas(w, h)
    if (typeof document !== 'undefined') {
      const c = document.createElement('canvas')
      c.width = w
      c.height = h
      return c
    }
  } catch { /* fall through */ }
  return null
}

function ctx2d(c: Canvasish): CanvasRenderingContext2D | null {
  try {
    return (c.getContext('2d') as CanvasRenderingContext2D | null) ?? null
  } catch {
    return null
  }
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error(`failed to load ${url}`))
    img.src = url
  })
}

function toCanvas(img: HTMLImageElement): Canvasish | null {
  const c = makeCanvas(img.naturalWidth, img.naturalHeight)
  const g = c && ctx2d(c)
  if (!c || !g) return null
  g.drawImage(img, 0, 0)
  return c
}

function rgbToHsl(r: number, g: number, b: number): [number, number, number] {
  r /= 255; g /= 255; b /= 255
  const max = Math.max(r, g, b), min = Math.min(r, g, b)
  const l = (max + min) / 2
  if (max === min) return [0, 0, l]
  const d = max - min
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
  let h = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4
  h *= 60
  return [h, s, l]
}

function hslToRgb(h: number, s: number, l: number): [number, number, number] {
  const c = (1 - Math.abs(2 * l - 1)) * s
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1))
  const m = l - c / 2
  const [r, g, b] = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x]
  return [Math.round((r + m) * 255), Math.round((g + m) * 255), Math.round((b + m) * 255)]
}

// The generated worker wears a teal shirt (#0c8384 family). Pixels in that hue
// band are the shirt; nothing else on the sprite is teal.
const SHIRT_HUE_MIN = 160
const SHIRT_HUE_MAX = 200
const SHIRT_REF_S = 0.83
const SHIRT_REF_L = 0.28

/**
 * Recolour the shirt pixels of RGBA data toward `target` (hex), keeping the
 * art's shading: hue is replaced, saturation and lightness shift by the
 * target's offset from the reference teal. Exported for tests.
 */
export function recolorShirt(data: Uint8ClampedArray, target: string): void {
  const m = /^#?([0-9a-f]{6})$/i.exec(target)
  if (!m) return
  const n = parseInt(m[1], 16)
  const [th, ts, tl] = rgbToHsl((n >> 16) & 255, (n >> 8) & 255, n & 255)
  for (let i = 0; i < data.length; i += 4) {
    if (data[i + 3] === 0) continue
    const [h, s, l] = rgbToHsl(data[i], data[i + 1], data[i + 2])
    if (s < 0.25 || h < SHIRT_HUE_MIN || h > SHIRT_HUE_MAX) continue
    const ns = Math.max(0, Math.min(1, s * (ts / SHIRT_REF_S)))
    const nl = Math.max(0.04, Math.min(0.9, l + (tl - SHIRT_REF_L) * 0.8))
    const [r, g, b] = hslToRgb(th, ns, nl)
    data[i] = r; data[i + 1] = g; data[i + 2] = b
  }
}

let pending: Promise<Sheets | null> | null = null

/** Load both sheets once. Resolves null when images or canvas are unavailable. */
export function loadOfficeSheets(): Promise<Sheets | null> {
  if (pending) return pending
  pending = (async () => {
    try {
      if (typeof Image === 'undefined') return null
      const [wImg, rImg] = await Promise.all([loadImage(workerUrl), loadImage(robotUrl)])
      const worker = toCanvas(wImg)
      const robot = toCanvas(rImg)
      if (!worker || !robot) return null
      const byShirt = new Map<string, Canvasish>()
      return {
        worker,
        robot,
        workerFor(variant: Variant): Canvasish {
          const shirt = variant.T
          if (!shirt) return worker
          const hit = byShirt.get(shirt)
          if (hit) return hit
          const c = makeCanvas(worker.width, worker.height)
          const g = c && ctx2d(c)
          if (!c || !g) return worker
          g.drawImage(worker as CanvasImageSource, 0, 0)
          const img = g.getImageData(0, 0, c.width, c.height)
          recolorShirt(img.data, shirt)
          g.putImageData(img, 0, 0)
          byShirt.set(shirt, c)
          return c
        },
      }
    } catch {
      return null
    }
  })()
  return pending
}
