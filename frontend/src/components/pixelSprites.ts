// Pixel-art sprite DATA for the office-floor dashboard visualization. Pure
// data + a frame-composition helper, no React and no renderer — consumed by
// officeScene.ts (canvas blitting) and covered by TaskFactory.test.tsx.
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

// Overlay [col,row,char] cells onto a direction's base rows.
function frameFor(dir: Dir, overlay: Array<[number, number, string]>, extraPalette: Record<string, string> = {}): Frame {
  const rows = DIR_ROWS[dir].map((r) => r.split(''))
  for (const [x, y, ch] of overlay) if (rows[y]) rows[y][x] = ch
  return { rows: rows.map((r) => r.join('')), palette: { ...BASE_PALETTE, ...extraPalette } }
}

const ARM = { a: '#f0a875' } // acting-arm recolor (skin) — retinted per variant

// 3-frame walk cycle: contact-left, passing (mid-stride, feet together), and
// contact-right. Applied only while moving. Cells clear to '.' where a foot
// lifts. Authored for the FRONT/BACK leg block (cols 3-11, rows 18-23).
const WALK_LEGS: Array<Array<[number, number, string]>> = [
  // left foot forward
  [[2, 22, 'B'], [3, 22, 'B'], [2, 23, 'o'], [3, 23, 'o'],
   [11, 22, '.'], [12, 22, '.'], [11, 23, '.'], [12, 23, '.'], [10, 21, 'B'], [11, 21, 'B']],
  // passing pose: both feet centered/together (mid-stride)
  [[5, 22, 'B'], [6, 22, 'B'], [9, 22, 'B'], [10, 22, 'B'],
   [5, 23, 'o'], [6, 23, 'o'], [9, 23, 'o'], [10, 23, 'o']],
  // right foot forward
  [[12, 22, 'B'], [13, 22, 'B'], [12, 23, 'o'], [13, 23, 'o'],
   [3, 22, '.'], [4, 22, '.'], [3, 23, '.'], [4, 23, '.'], [4, 21, 'B'], [5, 21, 'B']],
]

// Actions are authored against the FRONT body (worker faces viewer while
// using a tool). Right arm cols 12-13; hand/glove row 16.
const ACTION_FRAMES: Record<Exclude<Action, 'robot'>, Frame[]> = {
  idle: [
    frameFor('front', []),
    frameFor('front', [[7, 10, 'N'], [8, 10, 'N']]),
    frameFor('front', []),
    frameFor('front', [[5, 6, 'K'], [10, 6, 'K'], [6, 6, 'e'], [11, 6, 'e']]),
  ],
  drawing: [
    frameFor('front', [[12, 13, 'a'], [13, 14, 'a'], [13, 15, 'c'], [13, 16, 'c']], { ...ARM, c: '#fde68a' }),
    frameFor('front', [[12, 13, 'a'], [11, 14, 'a'], [10, 15, 'c'], [10, 16, 'c']], { ...ARM, c: '#fde68a' }),
    frameFor('front', [[12, 13, 'a'], [9, 14, 'a'], [8, 15, 'c'], [8, 16, 'c']], { ...ARM, c: '#fde68a' }),
    frameFor('front', [[12, 13, 'a'], [11, 14, 'a'], [10, 15, 'c'], [10, 16, 'c']], { ...ARM, c: '#fde68a' }),
  ],
  inspecting: [
    frameFor('front', [[12, 11, 'a'], [13, 9, 'a'], [12, 6, 'g'], [13, 6, 'g'], [12, 7, 'g'], [13, 7, 'g'], [13, 8, 'g']], { ...ARM, g: '#e2e8f0' }),
    frameFor('front', [[12, 11, 'a'], [10, 9, 'a'], [8, 6, 'g'], [9, 6, 'g'], [8, 7, 'g'], [9, 7, 'g'], [9, 8, 'g']], { ...ARM, g: '#e2e8f0' }),
    frameFor('front', [[12, 11, 'a'], [13, 9, 'a'], [12, 6, 'g'], [13, 6, 'g'], [12, 7, 'g'], [13, 7, 'g'], [13, 8, 'g']], { ...ARM, g: '#e2e8f0' }),
  ],
  hammering: [
    frameFor('front', [[12, 8, 'a'], [13, 6, 'a'], [12, 3, 'm'], [13, 3, 'm'], [12, 4, 'm'], [13, 4, 'm'], [13, 5, 'a']], { ...ARM, m: '#d6d3d1' }),
    frameFor('front', [[12, 11, 'a'], [13, 11, 'a'], [12, 12, 'm'], [13, 12, 'm'], [12, 13, 'm'], [13, 13, 'm']], { ...ARM, m: '#d6d3d1' }),
    frameFor('front', [[12, 14, 'a'], [13, 14, 'a'], [12, 15, 'm'], [13, 15, 'm'], [12, 16, 'm'], [13, 16, 'm']], { ...ARM, m: '#d6d3d1' }),
    frameFor('front', [[12, 11, 'a'], [13, 11, 'a'], [12, 12, 'm'], [13, 12, 'm'], [12, 13, 'm'], [13, 13, 'm']], { ...ARM, m: '#d6d3d1' }),
  ],
  testing: [
    frameFor('front', [[12, 11, 'a'], [13, 12, 'a'], [12, 8, 'f'], [13, 8, 'f'], [12, 9, 'f'], [13, 9, 'f'], [13, 7, 'f']], { ...ARM, f: '#2dd4bf' }),
    frameFor('front', [[12, 10, 'a'], [13, 11, 'a'], [11, 7, 'f'], [12, 7, 'f'], [11, 8, 'f'], [12, 8, 'f'], [12, 6, 'f']], { ...ARM, f: '#2dd4bf' }),
    frameFor('front', [[12, 11, 'a'], [13, 10, 'a'], [13, 6, 'f'], [13, 7, 'f'], [12, 7, 'f'], [12, 8, 'f'], [13, 8, 'f']], { ...ARM, f: '#2dd4bf' }),
    frameFor('front', [[12, 10, 'a'], [13, 11, 'a'], [11, 7, 'f'], [12, 7, 'f'], [11, 8, 'f'], [12, 8, 'f'], [12, 6, 'f']], { ...ARM, f: '#2dd4bf' }),
  ],
  approving: [
    frameFor('front', [[12, 8, 'a'], [13, 6, 'a'], [12, 3, 'p'], [13, 3, 'p'], [12, 4, 'p'], [13, 4, 'p'], [13, 5, 'p']], { ...ARM, p: '#f472b6' }),
    frameFor('front', [[12, 12, 'a'], [13, 12, 'a'], [12, 13, 'p'], [13, 13, 'p'], [12, 14, 'p'], [13, 14, 'p']], { ...ARM, p: '#f472b6' }),
    frameFor('front', [[12, 12, 'a'], [13, 12, 'a'], [12, 13, 'p'], [13, 13, 'p'], [12, 14, 'p'], [13, 14, 'p']], { ...ARM, p: '#f472b6' }),
  ],
  celebrating: [
    frameFor('front', [[13, 11, 'a'], [13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a'], [2, 11, 'a'], [2, 10, 'a'], [2, 9, 'a'], [2, 8, 'a']], ARM),
    frameFor('front', [[13, 11, 'a'], [13, 12, 'a'], [13, 13, 'a'], [13, 14, 'a'], [2, 11, 'a'], [2, 12, 'a'], [2, 13, 'a'], [2, 14, 'a']], ARM),
    frameFor('front', [[13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a'], [13, 6, 'a'], [2, 10, 'a'], [2, 9, 'a'], [2, 8, 'a'], [2, 6, 'a']], ARM),
    frameFor('front', [[13, 11, 'a'], [13, 12, 'a'], [13, 13, 'a'], [13, 14, 'a'], [2, 11, 'a'], [2, 12, 'a'], [2, 13, 'a'], [2, 14, 'a']], ARM),
  ],
  waving: [
    frameFor('front', [[13, 11, 'a'], [13, 12, 'a'], [13, 13, 'a'], [13, 14, 'a']], ARM),
    frameFor('front', [[13, 11, 'a'], [13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a']], ARM),
    frameFor('front', [[13, 10, 'a'], [13, 9, 'a'], [13, 7, 'a'], [13, 6, 'a']], ARM),
    frameFor('front', [[13, 11, 'a'], [13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a']], ARM),
  ],
}

// Plain directional standing/walking bodies (no tool) for when a sprite is
// walking or shown from side/back. Frame index only matters for the head-bob
// idle wobble on front.
const WALK_BODY: Record<Dir, Frame> = {
  front: frameFor('front', []),
  side: frameFor('side', []),
  back: frameFor('back', []),
}

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
function robotFrame(visorFill: string, antennaFill: string): Frame {
  const rows = ROBOT_ROWS_BASE.map((r) => r.split(''))
  for (let x = 0; x < GRID_W; x++) {
    if (rows[5][x] === 'V') rows[5][x] = 'v'
    if (rows[6][x] === 'V') rows[6][x] = 'v'
  }
  return { rows: rows.map((r) => r.join('')), palette: { ...ROBOT_PALETTE, v: visorFill, a: antennaFill } }
}
const ROBOT_FRAMES: Frame[] = [
  robotFrame('#312e81', '#a5b4fc'),
  robotFrame('#e0e7ff', '#f472b6'),
  robotFrame('#312e81', '#a5b4fc'),
]

export function actionFrameCount(action: Action): number {
  return action === 'robot' ? ROBOT_FRAMES.length : ACTION_FRAMES[action].length
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

// Compose the sprite for action + frame, optional walk-leg overlay, per-worker
// variant, and facing direction. While walking (walkLeg >= 0) or when not
// front-facing, we use the plain directional body instead of the tool action
// (you can't see someone hammering from behind). Robots ignore dir + walkLeg.
export function composeFrame(
  action: Action,
  frameIndex: number,
  walkLeg = -1,
  variant?: Variant,
  dir: Dir = 'front',
): Frame {
  if (action === 'robot') {
    return ROBOT_FRAMES[frameIndex % ROBOT_FRAMES.length]
  }

  let base: Frame
  if (walkLeg >= 0 || dir !== 'front') {
    // Walking, or facing side/back → plain directional body (+ walk legs).
    base = WALK_BODY[dir]
  } else {
    const frames = ACTION_FRAMES[action]
    base = frames[frameIndex % frames.length]
  }

  if (walkLeg >= 0) {
    const rows = base.rows.map((r) => r.split(''))
    // Side profile has a single leg column; only front/back use the L/R walk
    // overlay. For side, nudge the boot to fake a step.
    if (dir === 'side') {
      const shift = walkLeg % 3 === 1 ? 0 : walkLeg % 3 === 0 ? -1 : 1
      for (let y = 22; y <= 23; y++) {
        const r = rows[y]
        for (let x = 0; x < r.length; x++) if (r[x] === 'B' || r[x] === 'b' || r[x] === 'o') {
          const nx = x + shift
          if (nx >= 0 && nx < r.length && r[nx] === '.') { r[nx] = r[x]; r[x] = '.' }
        }
      }
    } else {
      for (const [x, y, ch] of WALK_LEGS[walkLeg % WALK_LEGS.length]) if (rows[y]) rows[y][x] = ch
    }
    base = { rows: rows.map((r) => r.join('')), palette: base.palette }
  }

  return applyVariant(base, variant)
}

export const WALK_FRAME_COUNT = WALK_LEGS.length
