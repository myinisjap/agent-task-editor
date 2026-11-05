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
    walk_east: [23, 8],
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
  // Worker sheet recoloured for a variant's shirt, hair and skin, built on first use.
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

export function rgbToHsl(r: number, g: number, b: number): [number, number, number] {
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

// Colour bands of the generated worker sheet (sampled from worker.png):
// - shirt: teal #0c8384 family, hue 160-200.
// - hair: browns #68371e / #4e2618 / #7a4f3c ..., low lightness. The boots use
//   exactly the same browns, so hair is also limited to rows above the neck.
// - skin: #f5b289 family, light and warm; its hue limit (25) keeps the
//   cardboard box / wooden handles (hue >= 26) out.
// Hair and skin split at lightness 0.40; the outline, trousers and purple
// boots have other hues, so the classes never overlap.
export const SHIRT_HUE_MIN = 160
export const SHIRT_HUE_MAX = 200
export const SHIRT_REF_S = 0.83
export const SHIRT_REF_L = 0.28
export const HAIR_REF_S = 0.55
export const HAIR_REF_L = 0.26
export const SKIN_REF_S = 0.84
export const SKIN_REF_L = 0.75
const DEFAULT_NECK = 22
const NECK_MIN_SHIRT_PX = 6

export const PX_NONE = 0
export const PX_SHIRT = 1
export const PX_HAIR = 2
export const PX_SKIN = 3

/** Which recolour class a pixel (original hsl) belongs to. Pure; exported for tests. */
export function classifyWorkerPixel(h: number, s: number, l: number, cellY: number, neckY: number, cellH: number): 0 | 1 | 2 | 3 {
  if (s >= 0.25 && h >= SHIRT_HUE_MIN && h <= SHIRT_HUE_MAX) return PX_SHIRT
  if (s < 0.3 || (h > 25 && h < 345)) return PX_NONE
  if (l >= 0.12 && l < 0.4) return cellY < neckY && (h <= 22 || h >= 340) ? PX_HAIR : PX_NONE
  if (l >= 0.4 && l < 0.9) return cellY < cellH - 8 ? PX_SKIN : PX_NONE
  return PX_NONE
}

type Target = { h: number; s: number; l: number; refS: number; refL: number }

function parseTarget(hex: string | undefined, refS: number, refL: number): Target | null {
  const m = hex ? /^#?([0-9a-f]{6})$/i.exec(hex) : null
  if (!m) return null
  const n = parseInt(m[1], 16)
  const [h, s, l] = rgbToHsl((n >> 16) & 255, (n >> 8) & 255, n & 255)
  return { h, s, l, refS, refL }
}

function recolorPixel(data: Uint8ClampedArray, i: number, s: number, l: number, t: Target): void {
  const ns = Math.max(0, Math.min(1, s * (t.s / t.refS)))
  // Very dark targets would flatten hair shading into a blob: compress the offset.
  const off = (t.l - t.refL) * (t.l < t.refL ? 0.6 : 0.8)
  const nl = Math.max(0.04, Math.min(0.9, l + off))
  const [r, g, b] = hslToRgb(t.h, ns, nl)
  data[i] = r; data[i + 1] = g; data[i + 2] = b
}

/**
 * Recolour the shirt pixels of RGBA data toward `target` (hex), keeping the
 * art's shading: hue is replaced, saturation and lightness shift by the
 * target's offset from the reference teal. Exported for tests.
 */
export function recolorShirt(data: Uint8ClampedArray, target: string): void {
  const t = parseTarget(target, SHIRT_REF_S, SHIRT_REF_L)
  if (!t) return
  for (let i = 0; i < data.length; i += 4) {
    if (data[i + 3] === 0) continue
    const [h, s, l] = rgbToHsl(data[i], data[i + 1], data[i + 2])
    if (s < 0.25 || h < SHIRT_HUE_MIN || h > SHIRT_HUE_MAX) continue
    recolorPixel(data, i, s, l, t)
  }
}

/**
 * Recolour shirt (variant.T), hair (variant.r) and skin (variant.K) of the
 * worker sheet in one pass: every pixel is classified from its ORIGINAL colour
 * so a recoloured shirt can never be picked up again as hair or skin.
 */
export function recolorWorker(
  data: Uint8ClampedArray, width: number, height: number, variant: Variant,
  cellW: number = WORKER_SHEET.cellW, cellH: number = WORKER_SHEET.cellH,
): void {
  const shirt = parseTarget(variant.T, SHIRT_REF_S, SHIRT_REF_L)
  const hair = parseTarget(variant.r, HAIR_REF_S, HAIR_REF_L)
  const skin = parseTarget(variant.K, SKIN_REF_S, SKIN_REF_L)
  if (!shirt && !hair && !skin) return
  for (let cy = 0; cy + cellH <= height; cy += cellH) {
    for (let cx = 0; cx + cellW <= width; cx += cellW) {
      // Neck line: first row of this cell with enough shirt pixels.
      let neck = DEFAULT_NECK
      for (let y = 0; y < cellH; y++) {
        let n = 0
        for (let x = 0; x < cellW; x++) {
          const i = ((cy + y) * width + cx + x) * 4
          if (data[i + 3] === 0) continue
          const [h, s] = rgbToHsl(data[i], data[i + 1], data[i + 2])
          if (s >= 0.25 && h >= SHIRT_HUE_MIN && h <= SHIRT_HUE_MAX) n++
        }
        if (n >= NECK_MIN_SHIRT_PX) { neck = y; break }
      }
      for (let y = 0; y < cellH; y++) {
        for (let x = 0; x < cellW; x++) {
          const i = ((cy + y) * width + cx + x) * 4
          if (data[i + 3] === 0) continue
          const [h, s, l] = rgbToHsl(data[i], data[i + 1], data[i + 2])
          const c = classifyWorkerPixel(h, s, l, y, neck, cellH)
          const t = c === PX_SHIRT ? shirt : c === PX_HAIR ? hair : c === PX_SKIN ? skin : null
          if (t) recolorPixel(data, i, s, l, t)
        }
      }
    }
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
      const byVariant = new Map<string, Canvasish>()
      return {
        worker,
        robot,
        workerFor(variant: Variant): Canvasish {
          if (!variant.T && !variant.r && !variant.K) return worker
          const key = `${variant.T ?? ''}|${variant.r ?? ''}|${variant.K ?? ''}`
          const hit = byVariant.get(key)
          if (hit) return hit
          const c = makeCanvas(worker.width, worker.height)
          const g = c && ctx2d(c)
          if (!c || !g) return worker
          g.drawImage(worker as CanvasImageSource, 0, 0)
          const img = g.getImageData(0, 0, c.width, c.height)
          recolorWorker(img.data, c.width, c.height, variant)
          g.putImageData(img, 0, 0)
          byVariant.set(key, c)
          return c
        },
      }
    } catch {
      return null
    }
  })()
  return pending
}
