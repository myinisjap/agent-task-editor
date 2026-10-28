// Pixel-art sprite DATA for the office-floor dashboard visualization. Pure
// data + a frame-composition helper, no React and no renderer — consumed by
// officeScene.ts (canvas blitting) and covered by taskBuckets.test.ts.
//
// Each sprite is a 16x24 grid of characters; a palette maps each char to a
// color (space and '.' are transparent). Characters are DIRECTIONAL: separate
// front / side / back base bodies, so a worker walking up shows their back and
// one walking sideways shows a profile — the single biggest "quality" lever
// over a lone flipped front view. Shading uses one coherent top-left light
// source (highlight up-left, shadow down-right). Per-worker VARIANTS recolor
// hat/hair/skin/shirt so a crowd reads as different people.

export type Action =
  | 'idle'
  | 'drawing'
  | 'inspecting'
  | 'hammering'
  | 'testing'
  | 'robot'
  | 'approving'
  | 'celebrating'
  | 'waving'

// Which way a sprite faces. 'side' is drawn facing right; officeScene mirrors
// it for leftward travel. Back is used while walking away / working at a
// station (facing up into it).
export type Dir = 'front' | 'side' | 'back'

export const DEFAULT_ACTIONS: Record<string, Action> = {
  not_ready: 'idle',
  plan: 'drawing',
  'review-plan': 'inspecting',
  work: 'hammering',
  testing: 'testing',
  'agent-review': 'robot',
  review: 'approving',
  done: 'celebrating',
}

export const BUCKET_ACTIONS: Record<'notReady' | 'agentWorking' | 'waitingHuman', Action> = {
  notReady: 'idle',
  agentWorking: 'robot',
  waitingHuman: 'waving',
}

// Legend (shared across directions):
//   h hat  H hat-hi  i hat-shade   r hair  q hair-hi  j hair-shade
//   K skin  S skin-shade  n nose  N neck  e eye
//   T shirt  t shirt-hi  u shirt-shade  A arm-skin  a arm-shade
//   G glove  P belt  k buckle  L legs  x leg-hi  l leg-shade  B boot  b boot-hi  o sole
//
// FRONT: faces the viewer. Light from top-left → left edges get *-hi chars,
// right edges get *-shade chars.
const FRONT_ROWS = [
  '......qrrj......', // 0  hair crown (hi-left, shade-right)
  '.....qrrrrrj....', // 1  hair
  '....qrrrrrrrj...', // 2  hair
  '....qrrrrrrrj...', // 3  hair
  '....qrKKKKrj....', // 4  hairline + forehead
  '....qKKKKKKj....', // 5  hair sides + face
  '....KeKKKKeK....', // 6  eyes
  '....KKKnKKKK....', // 7  cheeks + nose
  '....KKKKKKSS....', // 8  jaw (shade right)
  '.....KKKKSS.....', // 9  chin taper
  '......NNNN......', // 10 neck
  '..AAtTTTTTTuzA..', // 11 shoulders: arm + shirt(hi-left/shade-right) + arm
  '..AATTTTTTTTzA..', // 12 upper arm + shirt
  '..AATtTTTTuuzA..', // 13 arm + shirt hi-left / shade-right
  '..AATtTTTTuuzA..', // 14
  '..AGTtTTTTuuGa..', // 15 cuff + shirt
  '..GGTTTTTTTTGG..', // 16 gloves + shirt hem
  '....PPPkkPPP....', // 17 belt + buckle
  '....xLL..LLl....', // 18 upper leg (hi-left, shade-right)
  '....xLL..LLl....', // 19
  '....xLL..LLl....', // 20
  '....xLL..LLl....', // 21
  '...bBBB..BBBo...', // 22 boots (hi-left, sole-right)
  '...oooo..oooo...', // 23 soles
]

// SIDE: profile facing right. One near arm (right side of body) visible; nose
// juts right; back of head + hair on the left. Narrower torso.
const SIDE_ROWS = [
  '....qrrrj.......', // 0  hair crown
  '...qrrrrrj......', // 1  hair
  '..qrrrrrrrj.....', // 2  hair
  '..jrrrrrrrj.....', // 3  hair back + nape
  '..jrrKKKKKn.....', // 4  hair + face, nose bump right
  '..jrrKKKKKKn....', // 5  face + nose
  '...jrKKeKKKn....', // 6  eye (single, forward)
  '...jrKKKKKK.....', // 7  cheek
  '....rKKKKSS.....', // 8  jaw shade
  '.....KKKSS......', // 9  chin
  '......NNN.......', // 10 neck
  '.....tTTTTa.....', // 11 shoulder + near arm (right)
  '.....tTTTTAz....', // 12 torso + arm
  '.....tTTTTAz....', // 13
  '.....tTTTTAz....', // 14
  '.....tTTTuGa....', // 15 cuff
  '.....TTTuGG.....', // 16 hem + glove
  '.....PPkPP......', // 17 belt
  '.....xLLl.......', // 18 near leg (one column set in profile)
  '.....xLLl.......', // 19
  '.....xLLl.......', // 20
  '.....xLLl.......', // 21
  '....bBBBo.......', // 22 boot (toe points right)
  '....ooooo.......', // 23 sole
]

// BACK: facing away. Hair fills the whole head (no face/eyes); shoulders and
// back of shirt; legs/boots from behind. Light still top-left.
const BACK_ROWS = [
  '......qrrj......', // 0  hair crown
  '.....qrrrrrj....', // 1  hair
  '....qrrrrrrrj...', // 2  hair
  '....qrrrrrrrj...', // 3  hair
  '....qrrrrrrj....', // 4  hair
  '....qrrrrrrj....', // 5  hair
  '....qrrrrrrj....', // 6  hair (no eyes)
  '....qrrrrrrj....', // 7  hair nape
  '....jrrrrrrj....', // 8  nape shade
  '.....jrrrj......', // 9  nape taper
  '......NNNN......', // 10 neck
  '..AAtTTTTTTuzA..', // 11 shoulders + back
  '..AATTTTTTTTzA..', // 12
  '..AATtTTTTuuzA..', // 13
  '..AATtTTTTuuzA..', // 14
  '..AGTtTTTTuuGa..', // 15
  '..GGTTTTTTTTGG..', // 16
  '....PPPPPPPP....', // 17 belt (no buckle from behind)
  '....xLL..LLl....', // 18
  '....xLL..LLl....', // 19
  '....xLL..LLl....', // 20
  '....xLL..LLl....', // 21
  '...bBBB..BBBo...', // 22 boots (heels)
  '...oooo..oooo...', // 23
]

const DIR_ROWS: Record<Dir, string[]> = { front: FRONT_ROWS, side: SIDE_ROWS, back: BACK_ROWS }

// Default palette with a coherent top-left light model (‑hi lighter, ‑shade
// darker than the base tone). Chars in VARIANT_KEYS are overridden per worker;
// the derived hi/shade for variant colors are computed in applyVariant so a
// recolor keeps its shading.
const BASE_PALETTE: Record<string, string> = {
  h: '#facc15', H: '#fde047', i: '#ca8a04', I: '#ca8a04', // hat + hi + shade (I = shade alt)
  r: '#3f2d1d', q: '#5a4230', j: '#2a1c11', // hair + hi + shade
  K: '#f0a875', S: '#cf8a52', n: '#dd9a68', // skin + shade + nose
  e: '#1e293b', N: '#d99361', // eye, neck
  T: '#5b8def', t: '#83aef7', u: '#3a63b8', // shirt + hi + shade
  A: '#f0a875', z: '#cf8a52', a: '#f0a875', // arm + arm-shade + hand/acting-arm skin
  G: '#334155', P: '#475569', k: '#cbd5e1', // glove, belt, buckle
  L: '#3b4a63', x: '#4c5f7e', l: '#2b3648', // legs + hi + shade
  B: '#1e293b', b: '#2f3d52', o: '#0f172a', // boots + hi + sole
}

// Chars recolored per worker (base tones). Their -hi/-shade partners are
// derived automatically so the light model survives a recolor.
const VARIANT_KEYS = ['h', 'r', 'K', 'T', 'A'] as const
export type Variant = Partial<Record<(typeof VARIANT_KEYS)[number], string>>

// hi/shade partner char for each recolorable base char.
const SHADE_OF: Record<string, { hi?: string; shade: string; extra?: string[] }> = {
  h: { hi: 'H', shade: 'i', extra: ['I'] }, // I is an alt hat-shade cell
  r: { hi: 'q', shade: 'j' },
  K: { shade: 'S', extra: ['n', 'N'] }, // skin drives nose + neck too
  T: { hi: 't', shade: 'u' },
  A: { shade: 'z' }, // arm base + arm-shade (body arm-shade char is 'z')
}

// A palette of distinct looks. officeScene picks one per sprite so a station's
// crew looks like different people.
// Hats are gone, so hair color is now the main head variety — every variant
// sets a distinct hair (r) so a crew doesn't read as clones.
export const VARIANTS: Variant[] = [
  { r: '#3f2d1d' }, // dark brown hair, tan skin, blue shirt
  { r: '#7a4a1e', T: '#e05a4e' }, // light brown hair, red shirt
  { K: '#8d5a3c', r: '#1c1310', T: '#334155', A: '#8d5a3c' }, // deep skin, black hair, slate shirt
  { r: '#a0521a', K: '#e8b98f', T: '#a855f7', A: '#e8b98f' }, // auburn hair, purple shirt
  { K: '#c98a5e', r: '#c99a3a', T: '#f59e0b', A: '#c98a5e' }, // dark-blond hair, amber shirt
  { r: '#1c1917', T: '#14b8a6' }, // black hair, teal shirt
  { K: '#6f4426', r: '#0c0a09', T: '#6366f1', A: '#6f4426' }, // dark skin, black hair, indigo shirt
]

export const GRID_W = FRONT_ROWS[0].length
export const GRID_H = FRONT_ROWS.length

export type Frame = { rows: string[]; palette: Record<string, string> }

// Lighten / darken a hex color by a factor for auto-derived hi/shade tones.
function shift(hex: string, f: number): string {
  const m = hex.replace('#', '')
  const r = parseInt(m.slice(0, 2), 16)
  const g = parseInt(m.slice(2, 4), 16)
  const b = parseInt(m.slice(4, 6), 16)
  const c = (v: number) => Math.max(0, Math.min(255, Math.round(f >= 1 ? v + (255 - v) * (f - 1) : v * f)))
  return '#' + [c(r), c(g), c(b)].map((v) => v.toString(16).padStart(2, '0')).join('')
}


// ---------------------------------------------------------------------------
// Overlays. Frames are NOT baked: composeFrame builds the base rows (direction
// body, then the robot head if any) and only then paints these [col,row,char]
// cells on top, so tools survive robot mode (the head used to be swapped in
// AFTER the overlays and erased raised tools).
type Cell = [number, number, string]
type Overlay = { cells: Cell[]; glance?: -1 | 1 }
export type { Overlay }

// Extra palette chars used by overlays; merged into every humanoid frame.
//   a acting arm (skin)  c pen  g magnifier glass  m hammer  f flask
//   p stamp  s spark  y bubble
const OVERLAY_PALETTE: Record<string, string> = {
  a: '#f0a875', c: '#fde68a', g: '#e2e8f0', m: '#d6d3d1', f: '#2dd4bf',
  p: '#f472b6', s: '#fde047', y: '#bae6fd',
}

const vline = (x: number, y0: number, y1: number, ch: string): Cell[] => {
  const out: Cell[] = []
  for (let y = y0; y <= y1; y++) out.push([x, y, ch])
  return out
}
const rect = (x: number, y: number, w: number, h: number, ch: string): Cell[] => {
  const out: Cell[] = []
  for (let j = 0; j < h; j++) for (let i = 0; i < w; i++) out.push([x + i, y + j, ch])
  return out
}
const ov = (...parts: Cell[][]): Overlay => ({ cells: parts.flat() })

type Tooled = Exclude<Action, 'robot'>

// Front-facing action loops (6-8 frames each: anticipation, hold, follow-through).
// Right arm is cols 12-13; the head occupies cols 4-11 so tools at cols 12-13
// never collide with it (in robot mode too).
const penFrame = (px: number, lift: number): Overlay =>
  ov([[12, 13, 'a'], [(12 + px) >> 1, 14, 'a']], [[px, 15 - lift, 'c'], [px, 16 - lift, 'c']])
const lensFrame = (gx: number, up: number): Overlay =>
  ov([[12, 11, 'a'], [Math.min(13, gx + 2), 9 - up, 'a']], rect(gx, 6 - up, 2, 2, 'g'), [[gx + 1, 8 - up, 'g']])

export const ACTION_OVERLAYS: Record<Tooled, Overlay[]> = {
  // breathing/glancing: look left, hold, look right, hold
  idle: [
    ov(), ov(), { cells: [], glance: -1 }, { cells: [], glance: -1 },
    ov(), ov(), { cells: [], glance: 1 }, { cells: [], glance: 1 },
  ],
  // pen zigzags across the paper, then lifts
  drawing: [penFrame(12, 0), penFrame(10, 0), penFrame(8, 0), penFrame(9, 0), penFrame(11, 0), penFrame(9, 0), penFrame(10, 1), penFrame(12, 1)],
  // magnifier sweeps right → centre → left, then leans in for a closer look
  inspecting: [lensFrame(12, 0), lensFrame(10, 0), lensFrame(8, 0), lensFrame(8, 1), lensFrame(8, 1), lensFrame(10, 0), lensFrame(12, 0), lensFrame(12, 0)],
  hammering: [
    ov(vline(13, 11, 13, 'a'), rect(12, 8, 2, 3, 'm')), // wind-up
    ov(vline(13, 9, 12, 'a'), rect(12, 5, 2, 3, 'm')), // raise
    ov(vline(13, 7, 11, 'a'), rect(12, 2, 2, 3, 'm')), // peak
    ov(vline(13, 7, 11, 'a'), rect(12, 2, 2, 3, 'm')), // peak hold
    ov(vline(13, 12, 14, 'a'), rect(12, 15, 2, 3, 'm')), // strike
    ov(vline(13, 12, 14, 'a'), rect(12, 15, 2, 3, 'm'), [[14, 17, 's'], [15, 18, 's'], [14, 19, 's']]), // impact spark
    ov(vline(13, 11, 13, 'a'), rect(12, 12, 2, 3, 'm')), // recoil
    ov(vline(13, 11, 13, 'a'), rect(12, 10, 2, 3, 'm')),
  ],
  testing: [
    ov([[12, 12, 'a'], [13, 12, 'a']], rect(12, 10, 2, 2, 'f'), [[13, 9, 'f']]), // low
    ov([[12, 11, 'a'], [13, 10, 'a']], rect(12, 7, 2, 3, 'f'), [[13, 6, 'f']]), // raise
    ov([[12, 10, 'a'], [13, 11, 'a']], rect(11, 7, 2, 2, 'f'), [[12, 6, 'f']]), // swirl left
    ov([[12, 11, 'a'], [13, 10, 'a']], rect(12, 7, 2, 2, 'f'), [[13, 6, 'f'], [14, 7, 'f']]), // swirl right
    ov([[12, 10, 'a'], [13, 11, 'a']], rect(11, 7, 2, 2, 'f'), [[12, 6, 'f'], [13, 4, 'y']]), // swirl left + bubble
    ov([[12, 11, 'a'], [13, 10, 'a']], rect(12, 7, 2, 2, 'f'), [[13, 6, 'f'], [14, 7, 'f'], [12, 3, 'y'], [13, 4, 'y']]), // swirl right + bubbles
    ov([[12, 11, 'a'], [13, 10, 'a']], rect(12, 7, 2, 3, 'f'), [[13, 6, 'f'], [13, 3, 'y']]), // upright hold
    ov([[12, 12, 'a'], [13, 11, 'a']], rect(12, 9, 2, 2, 'f'), [[13, 8, 'f']]), // lower
  ],
  approving: [
    ov(vline(13, 12, 13, 'a'), rect(12, 14, 2, 2, 'p')), // rest
    ov(vline(13, 9, 12, 'a'), rect(12, 6, 2, 3, 'p')), // raise
    ov(vline(13, 6, 11, 'a'), rect(12, 2, 2, 3, 'p')), // peak
    ov(vline(13, 6, 11, 'a'), rect(12, 2, 2, 3, 'p')), // hold
    ov(vline(13, 12, 14, 'a'), rect(12, 15, 2, 2, 'p')), // slam
    ov(vline(13, 12, 14, 'a'), rect(12, 15, 2, 1, 'p'), rect(11, 16, 4, 1, 'p')), // pressed
    ov(vline(13, 12, 14, 'a'), rect(12, 15, 2, 1, 'p'), rect(11, 16, 4, 1, 'p')), // pressed hold
    ov(vline(13, 10, 13, 'a'), rect(12, 11, 2, 3, 'p')), // lift
  ],
  celebrating: [
    ov(vline(13, 11, 14, 'a'), vline(2, 11, 14, 'a')), // arms down
    ov(vline(13, 12, 14, 'a'), vline(2, 12, 14, 'a')), // crouch
    ov(vline(13, 9, 11, 'a'), vline(2, 9, 11, 'a')), // launch
    ov(vline(13, 6, 11, 'a'), vline(2, 6, 11, 'a')), // jump peak
    ov(vline(13, 6, 11, 'a'), vline(2, 6, 11, 'a')),
    ov(vline(13, 9, 11, 'a'), vline(2, 9, 11, 'a')), // falling
    ov(vline(13, 12, 14, 'a'), vline(2, 12, 14, 'a')), // land
    ov(vline(13, 10, 12, 'a'), vline(2, 11, 14, 'a')),
  ],
  waving: [
    ov(vline(13, 11, 14, 'a')),
    ov(vline(13, 8, 11, 'a')),
    ov(vline(13, 5, 11, 'a')), // raised hand hold
    ov(vline(13, 8, 11, 'a'), [[12, 7, 'a'], [12, 6, 'a'], [13, 7, 'a']]), // wave left
    ov(vline(13, 8, 11, 'a'), [[14, 7, 'a'], [14, 6, 'a'], [13, 7, 'a']]), // wave right
    ov(vline(13, 8, 11, 'a'), [[12, 7, 'a'], [12, 6, 'a'], [13, 7, 'a']]),
    ov(vline(13, 8, 11, 'a'), [[14, 7, 'a'], [14, 6, 'a'], [13, 7, 'a']]),
    ov(vline(13, 5, 11, 'a')), // raised hand hold
  ],
}

// Back-view work frames (4 each): worker at the workstation, facing up into it.
// Arms move on the back body (cols 2-3 / 12-13, rows 8-16); tool colour peeks
// above the shoulder for hammering / approving / testing.
export const BACK_WORK_OVERLAYS: Record<Tooled, Overlay[]> = {
  idle: [ov(), ov([[13, 16, '.'], [13, 15, 'G']]), ov(), ov([[2, 16, '.'], [2, 15, 'G']])],
  drawing: [0, 1, 2, 1].map((k) => ov([[13 - k, 15, 'a'], [13 - k, 16, 'c']])),
  inspecting: [0, 1, 2, 1].map((k) => ov(vline(13, 12 - k, 14, 'a'))),
  hammering: [
    ov(vline(13, 12, 14, 'a')),
    ov(vline(13, 10, 13, 'a'), rect(12, 7, 2, 3, 'm')),
    ov(vline(13, 9, 12, 'a'), rect(12, 5, 2, 3, 'm')),
    ov(vline(13, 12, 14, 'a'), rect(12, 11, 2, 2, 'm')),
  ],
  testing: [
    ov(vline(13, 12, 14, 'a')),
    ov(vline(13, 10, 13, 'a'), rect(12, 8, 2, 2, 'f')),
    ov(vline(13, 10, 12, 'a'), rect(11, 7, 2, 2, 'f')),
    ov(vline(13, 10, 12, 'a'), rect(12, 6, 2, 2, 'f'), [[13, 4, 'y']]),
  ],
  approving: [
    ov(vline(13, 12, 14, 'a')),
    ov(vline(13, 9, 12, 'a'), rect(12, 6, 2, 3, 'p')),
    ov(vline(13, 8, 11, 'a'), rect(12, 4, 2, 3, 'p')),
    ov(vline(13, 13, 15, 'a'), rect(12, 14, 2, 2, 'p')),
  ],
  celebrating: [
    ov(vline(13, 8, 11, 'a'), vline(2, 8, 11, 'a')),
    ov(vline(13, 6, 11, 'a'), vline(2, 8, 11, 'a')),
    ov(vline(13, 8, 11, 'a'), vline(2, 6, 11, 'a')),
    ov(vline(13, 6, 11, 'a'), vline(2, 6, 11, 'a')),
  ],
  waving: [
    ov(vline(13, 6, 11, 'a'), [[12, 5, 'a']]),
    ov(vline(13, 6, 11, 'a'), [[14, 5, 'a']]),
    ov(vline(13, 6, 11, 'a'), [[12, 5, 'a']]),
    ov(vline(13, 6, 11, 'a'), [[14, 5, 'a']]),
  ],
}

// Seconds per action frame (hammering is snappy, idle is lazy). officeScene
// adds a small per-sprite jitter so a crew doesn't animate in lockstep.
const ACTION_DUR: Record<Action, number> = {
  idle: 0.35, drawing: 0.16, inspecting: 0.2, hammering: 0.11, testing: 0.16,
  robot: 0.14, approving: 0.13, celebrating: 0.13, waving: 0.14,
}
export function actionFrameDur(action: Action): number {
  return ACTION_DUR[action]
}

// Vertical body offset (art pixels) per action frame; only celebrating jumps.
export const ACTION_BOB: Partial<Record<Action, number[]>> = {
  celebrating: [0, 1, 0, -1, -1, 0, 1, 0],
}
export function actionBob(action: Action, frameIndex: number): number {
  const t = ACTION_BOB[action]
  return t ? t[((frameIndex % t.length) + t.length) % t.length] : 0
}

// Walk cycle: contact-L, pass, contact-R, pass (4 frames per direction). Legs
// sit in cols 3-12 / rows 18-23 for front and back. Cells '.' clear a foot.
const WALK_LEGS: Cell[][] = [
  // contact: left foot forward, right foot lifted
  [[2, 22, 'B'], [3, 22, 'B'], [2, 23, 'o'], [3, 23, 'o'],
   [11, 22, '.'], [12, 22, '.'], [11, 23, '.'], [12, 23, '.'], [10, 21, 'B'], [11, 21, 'B']],
  // pass: feet together under the body, right foot lifting
  [[3, 22, '.'], [4, 22, '.'], [11, 22, '.'], [12, 22, '.'], [3, 23, '.'], [4, 23, '.'], [11, 23, '.'], [12, 23, '.'],
   [9, 21, 'B'], [10, 21, 'B'], [9, 22, 'o'], [10, 22, 'o'], [9, 23, '.'], [10, 23, '.']],
  // contact: right foot forward, left foot lifted
  [[12, 22, 'B'], [13, 22, 'B'], [12, 23, 'o'], [13, 23, 'o'],
   [3, 22, '.'], [4, 22, '.'], [3, 23, '.'], [4, 23, '.'], [4, 21, 'B'], [5, 21, 'B']],
  // pass: left foot lifting
  [[3, 22, '.'], [4, 22, '.'], [11, 22, '.'], [12, 22, '.'], [3, 23, '.'], [4, 23, '.'], [11, 23, '.'], [12, 23, '.'],
   [5, 21, 'B'], [6, 21, 'B'], [5, 22, 'o'], [6, 22, 'o'], [5, 23, '.'], [6, 23, '.']],
]

// Side profile (facing right): authored strides instead of shifting boots.
// Far leg is shaded ('l' leg, 'B' boot).
const SIDE_WALK_LEGS: Cell[][] = [
  // near leg trails, far leg reaches forward
  [[7, 22, '.'], [8, 22, '.'], [7, 23, '.'], [8, 23, '.'],
   [8, 18, 'l'], [8, 19, 'l'], [9, 20, 'l'], [9, 21, 'l'], [9, 22, 'B'], [10, 22, 'B'], [9, 23, 'o'], [10, 23, 'o']],
  // pass: legs together
  [[4, 23, '.'], [8, 23, '.']],
  // far leg trails, near boot plants forward
  [[4, 20, 'l'], [4, 21, 'l'], [2, 22, 'B'], [3, 22, 'B'], [2, 23, 'o'], [3, 23, 'o'], [9, 22, 'B'], [9, 23, 'o']],
  // pass: legs together, boot lifting
  [[4, 22, '.'], [4, 23, '.'], [8, 22, '.'], [8, 23, '.']],
]

// Body bob (art-pixel y offset) per walk frame: raised on the pass frames.
export const WALK_BOB: number[] = [0, -1, 0, -1]
export const WALK_FRAME_COUNT = WALK_LEGS.length

// Robot (unchanged shape; still faces the viewer, ignores direction).
const ROBOT_ROWS_BASE = [
  '.......a........',
  '.......a........',
  '.......n........',
  '....rrrrrrrr....',
  '...rVVVVVVVVr...',
  '...rVVVVVVVVr...',
  '...rVVVVVVVVr...',
  '...rrrrrrrrrr...',
  '......RRRRRR....',
  '.jM..RRRRRR..Mj.',
  'jMM..RRRRRR..MMj',
  'jMM..RRRRRR..MMj',
  '.jM..RRRRRR..Mj.',
  '......RRRRRR....',
  '.....RRRRRRRR...',
  '.....RRRRRRRR...',
  '.....rrrrrrrr...',
  '....dddddddddd..',
  '...dDdDdDdDdDd..',
  '...wwwwwwwwww...',
  '...wDwDwDwDwD...',
  '...wwwwwwwwww...',
  '....DDDDDDDD....',
  '................',
]
const ROBOT_PALETTE = {
  a: '#a5b4fc', n: '#818cf8', r: '#6366f1', V: '#e0e7ff', R: '#4f46e5',
  M: '#818cf8', j: '#3730a3', d: '#3730a3', D: '#1e1b4b', w: '#312e81',
}

// Robot action loop: a bright visor pixel-column scans left→right→left and the
// antenna blinks every other frame.
function robotFrame(scanCol: number, antennaOn: boolean): Frame {
  const rows = ROBOT_ROWS_BASE.map((r) => r.split(''))
  for (let x = 0; x < GRID_W; x++) {
    for (const y of [5, 6]) if (rows[y][x] === 'V') rows[y][x] = x === scanCol ? 'Y' : 'v'
  }
  return {
    rows: rows.map((r) => r.join('')),
    palette: { ...ROBOT_PALETTE, v: '#312e81', Y: '#e0e7ff', a: antennaOn ? '#f472b6' : '#a5b4fc' },
  }
}
const ROBOT_FRAMES: Frame[] = [4, 5, 7, 9, 11, 10, 8, 6].map((c, i) => robotFrame(c, i % 2 === 1))

export function actionFrameCount(action: Action): number {
  return action === 'robot' ? ROBOT_FRAMES.length : ACTION_OVERLAYS[action].length
}

// Recolor a frame's palette for one worker, deriving matching -hi/-shade tones
// so the light model survives. Skips robots (no variant keys).
function applyVariant(f: Frame, variant?: Variant): Frame {
  if (!variant) return f
  const palette: Record<string, string> = { ...f.palette }
  for (const key of VARIANT_KEYS) {
    const base = variant[key]
    if (!base) continue
    palette[key] = base
    const rule = SHADE_OF[key]
    if (rule.hi) palette[rule.hi] = shift(base, 1.28)
    palette[rule.shade] = shift(base, 0.72)
    for (const ex of rule.extra ?? []) {
      const f = ex === 'N' ? 0.86 : ex === 'n' ? 0.94 : 0.72 // neck / nose / else shade
      palette[ex] = shift(base, f)
    }
  }
  // The acting-arm char 'a' doubles as the tool-holding skin arm in ACTION
  // overlays; keep it matched to the worker's skin (variant K) rather than the
  // shirt-arm-shade, so a raised tool arm isn't a mismatched color.
  if (variant.K) palette.a = variant.K
  return { rows: f.rows, palette }
}

// Robot restyle: reuse the exact humanoid body (so every action/walk overlay
// works) but recolor its chars to metal/chrome and turn the face into a glowing
// visor. Metallic palette layered over whatever the frame already has; the
// acting-arm char 'a' becomes chrome so a raised tool arm matches the body.
const ROBOT_SKIN: Record<string, string> = {
  // robot head: M metal shell, C casing-shade, V visor glass, e antenna/eye glow
  M: '#9aa7b8', C: '#5b6673', V: '#0e7490', e: '#5eead4',
  n: '#7c8b9e', N: '#475569',
  // (legacy human head chars, in case any survive) → chrome
  K: '#94a3b8', S: '#64748b',
  r: '#475569', q: '#64748b', j: '#334155',
  // shirt/torso → painted chassis (kept variant-tintable via T below)
  T: '#5b8def', t: '#83aef7', u: '#3a63b8',
  // arms → chrome
  A: '#cbd5e1', z: '#94a3b8', a: '#cbd5e1',
  // gloves/belt/legs/boots → dark metal + joints
  G: '#334155', P: '#1e293b', k: '#22d3ee', L: '#475569', x: '#64748b', l: '#334155',
  B: '#1e293b', b: '#334155', o: '#0f172a',
}

// Replace the whole head (rows 0-10) with a boxy robot head: an antenna on a
// stalk, a squared metal casing ('M' shell, 'C' casing-shade), a wide glowing
// visor ('V'), and a neck. Authored per direction so it reads unmistakably as
// a robot rather than a chrome person. Rows 11+ (body) keep the human layout so
// every action/walk overlay still lands.
const ROBOT_HEAD: Record<Dir, string[]> = {
  front: [
    '.......e........', // 0  antenna tip (glow)
    '.......M........', // 1  antenna stalk
    '.....MMMMMM.....', // 2  casing top
    '....MMMMMMMM....', // 3  casing
    '....MVVVVVVM....', // 4  visor top
    '....MVVVVVVM....', // 5  visor (eyes glow within)
    '....MVeVVeVM....', // 6  visor with two lit "eyes"
    '....MVVVVVVM....', // 7  visor bottom
    '....CMMMMMMC....', // 8  chin casing (shaded ends)
    '.....CMMMC......', // 9  jaw taper
    '......NNNN......', // 10 neck
  ],
  side: [
    '.....e..........', // 0  antenna tip
    '.....M..........', // 1  stalk
    '...MMMMMM.......', // 2  casing top
    '..MMMMMMMM......', // 3  casing
    '..CMVVVVVn......', // 4  visor + sensor nub (front-right)
    '..CMVVVVVVn.....', // 5  visor
    '..CMVeVVVVn.....', // 6  visor with lit eye
    '..CMVVVVVV......', // 7  visor bottom
    '...CMMMMMC......', // 8  jaw
    '....CMMMC.......', // 9  chin
    '......NNN.......', // 10 neck
  ],
  back: [
    '.......e........', // 0  antenna tip
    '.......M........', // 1  stalk
    '.....MMMMMM.....', // 2  casing top
    '....MMMMMMMM....', // 3  casing
    '....MCCCCCCM....', // 4  back panel (recessed)
    '....MCCCCCCM....', // 5
    '....MCC..CCM....', // 6  cooling vents
    '....MCC..CCM....', // 7
    '....CMMMMMMC....', // 8
    '.....CMMMC......', // 9
    '......NNNN......', // 10 neck
  ],
}

function robotHead(rows: string[][], dir: Dir) {
  const head = ROBOT_HEAD[dir]
  for (let y = 0; y < head.length; y++) {
    if (rows[y]) rows[y] = head[y].split('')
  }
}

export type ComposeOpts = {
  blink?: boolean // eyes closed this frame (front/side only)
  breath?: boolean // head/shoulder settle by 1 px (idle breathing)
}

// Close the eyes: 'e' cells in the face rows become skin (human) / dim visor (robot).
function applyBlink(rows: string[][], robot: boolean) {
  const fill = robot ? 'V' : 'K'
  for (let y = 4; y <= 7; y++) for (let x = 0; x < rows[y].length; x++) if (rows[y][x] === 'e') rows[y][x] = fill
}

// Slide the eyes one cell sideways (idle glance), staying inside the face.
function applyGlance(rows: string[][], robot: boolean, shiftX: number) {
  const fill = robot ? 'V' : 'K'
  const r = rows[6]
  const eyes: number[] = []
  r.forEach((c, x) => { if (c === 'e') eyes.push(x) })
  for (const x of eyes) {
    const nx = x + shiftX
    if (r[nx] === fill) { r[x] = fill; r[nx] = 'e' }
  }
}

// Drop head rows 0-9 down one row (new[1..10] = old[0..9]); row 0 empties.
function applyBreath(rows: string[][]) {
  for (let y = 10; y >= 1; y--) rows[y] = rows[y - 1]
  rows[0] = new Array(GRID_W).fill('.')
}

function paint(rows: string[][], cells: Cell[]) {
  for (const [x, y, ch] of cells) if (rows[y] && x >= 0 && x < GRID_W) rows[y][x] = ch
}

const frameCache = new Map<string, Frame>()

// Compose the sprite for action + frame, optional walk-leg overlay, per-worker
// variant, facing direction, and whether to render as a robot. Layers: base
// body → (robot head) → blink/glance/breath → walk legs OR action/back-work
// overlay. Front-facing parked sprites show the tool action; parked sprites
// facing back show BACK_WORK_OVERLAYS (so the worker at the desk visibly
// works); walking uses the walk cycle. The standalone `robot` action keeps its
// distinct scanning sprite. Results are memoised (bounded) — callers must not
// mutate the returned frame.
export function composeFrame(
  action: Action,
  frameIndex: number,
  walkLeg = -1,
  variant?: Variant,
  dir: Dir = 'front',
  robot = false,
  opts: ComposeOpts = {},
): Frame {
  if (action === 'robot') {
    return ROBOT_FRAMES[frameIndex % ROBOT_FRAMES.length]
  }
  const walking = walkLeg >= 0
  const blink = !!opts.blink && dir !== 'back'
  const breath = !!opts.breath && !walking
  const vi = variant ? VARIANTS.indexOf(variant) : -2
  let key = ''
  if (vi !== -1) {
    const n = walking ? 0 : dir === 'front' ? ACTION_OVERLAYS[action].length : dir === 'back' ? 4 : 1
    const fi = walking || dir === 'side' ? 0 : ((frameIndex % n) + n) % n
    key = `${action}|${fi}|${walkLeg}|${vi}|${dir}|${robot ? 1 : 0}|${blink ? 1 : 0}|${breath ? 1 : 0}`
    const hit = frameCache.get(key)
    if (hit) return hit
  }

  const rows = DIR_ROWS[dir].map((r) => r.split(''))
  if (robot) robotHead(rows, dir)
  let glance: -1 | 0 | 1 = 0
  let overlay: Cell[] = []
  if (walking) {
    overlay = (dir === 'side' ? SIDE_WALK_LEGS : WALK_LEGS)[walkLeg % WALK_LEGS.length]
  } else if (dir === 'front') {
    const frames = ACTION_OVERLAYS[action]
    const o = frames[((frameIndex % frames.length) + frames.length) % frames.length]
    overlay = o.cells
    glance = o.glance ?? 0
  } else if (dir === 'back') {
    const frames = BACK_WORK_OVERLAYS[action]
    overlay = frames[((frameIndex % frames.length) + frames.length) % frames.length].cells
  }
  if (blink) applyBlink(rows, robot)
  if (glance && !blink) applyGlance(rows, robot, glance)
  if (breath) applyBreath(rows)
  paint(rows, overlay)

  const composed: Frame = { rows: rows.map((r) => r.join('')), palette: { ...BASE_PALETTE, ...OVERLAY_PALETTE } }
  const themed = applyVariant(composed, variant)
  let out: Frame = themed
  if (robot) {
    // Metallic recolor on top of any variant tint. The chassis (T) still takes
    // the variant color so a robot crew varies in body paint; the head casing can
    // pick up a faint variant tint on the visor for extra variety.
    const palette = { ...themed.palette, ...ROBOT_SKIN }
    if (variant?.T) {
      palette.T = variant.T; palette.t = shift(variant.T, 1.28); palette.u = shift(variant.T, 0.72)
      palette.V = shift(variant.T, 0.5) // visor tinted toward the chassis color
    }
    palette.a = ROBOT_SKIN.a
    if (breath) palette.e = '#99f6e4' // antenna pulse
    out = { rows: themed.rows, palette }
  }
  if (key) {
    if (frameCache.size > 4000) frameCache.clear()
    frameCache.set(key, out)
  }
  return out
}
