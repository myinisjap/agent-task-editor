// Pixel-art sprite DATA for the office-floor dashboard visualization. Pure
// data + a frame-composition helper, no React and no renderer — consumed by
// officeScene.ts (canvas blitting) and covered by TaskFactory.test.tsx.
//
// Each sprite is a 16x24 grid of characters; a palette maps each char to a
// color (space and '.' are transparent). Every humanoid action shares one
// authored, shaded BASE body and overlays only the acting limb + tool per
// frame. Per-worker VARIANTS recolor hat/hair/shirt/skin so a crowd reads as
// different people rather than clones.

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

// Shared humanoid base (16 wide x 24 tall). Character legend:
//   h hat  i hat-shade  r hair
//   H skin  S skin-shade  N neck  e eye
//   T shirt  t shirt-hi  u shirt-shade  A arm-skin
//   G glove/cuff  P belt  k buckle  L legs  l leg-shade  B boot  o boot-sole
// Arms run full torso height (cols 2-3 and 12-13) so they read as limbs;
// per-action overlays replace the right arm (cols 12-13) + tool.
const BASE_ROWS = [
  '......iiii......', // 0  hat top shade
  '.....hhhhhh.....', // 1  hat crown
  '....hhhhhhhh....', // 2  hat crown
  '...iihhhhhhii...', // 3  hat brim (shaded ends)
  '....rrHHHHrr....', // 4  hairline + forehead
  '....rHHHHHHr....', // 5  hair sides + face
  '....HeHHHHeH....', // 6  eyes
  '....HHHHHHHH....', // 7  cheeks
  '....SHHHHHHS....', // 8  jaw (shaded sides)
  '.....SHHHHS.....', // 9  chin taper
  '......NNNN......', // 10 neck
  '..AAtTTTTTTtAA..', // 11 shoulders: arm + shirt(hi edges) + arm
  '..AATTTTTTTTAA..', // 12 upper arm + shirt
  '..AATuTTTTuTAA..', // 13 arm + shirt with side shadow
  '..AATuTTTTuTAA..', // 14 forearm + shirt
  '..AGTuTTTTuTGA..', // 15 cuff + shirt
  '..GGTTTTTTTTGG..', // 16 gloves/hands + shirt hem
  '....PPPkkPPP....', // 17 belt + buckle
  '....LLL..LLL....', // 18 hips/upper leg (leg gap)
  '....LLL..LLL....', // 19 upper leg
  '....LlL..LlL....', // 20 lower leg (inner shade)
  '....LLL..LLL....', // 21 lower leg
  '...BBBB..BBBB...', // 22 boots
  '...oooo..oooo...', // 23 boot soles
]

// Default palette. Chars listed in VARIANT_KEYS are overridden per-worker.
const BASE_PALETTE: Record<string, string> = {
  h: '#facc15', // hat
  i: '#ca8a04', // hat shade
  r: '#3f2d1d', // hair
  H: '#f0a875', // skin
  S: '#d68a56', // skin shade
  e: '#1e293b', // eye
  N: '#d99361', // neck
  T: '#5b8def', // shirt
  t: '#7ba7f5', // shirt highlight
  u: '#3f6cc4', // shirt shadow
  A: '#f0a875', // arm skin
  G: '#334155', // glove/cuff
  P: '#475569', // belt
  k: '#cbd5e1', // buckle
  L: '#3b4a63', // legs
  l: '#2b3648', // leg inner shade
  B: '#1e293b', // boots
  o: '#0f172a', // boot soles
}

// Chars recolored per worker. A variant supplies a subset; anything it omits
// falls back to BASE_PALETTE.
const VARIANT_KEYS = ['h', 'i', 'r', 'H', 'S', 'N', 'A', 'T', 't', 'u'] as const

export type Variant = Partial<Record<(typeof VARIANT_KEYS)[number], string>>

// A palette of distinct looks: hat/hair/skin/shirt combos. officeScene picks
// one per sprite (by index) so a station's crew looks like different people.
export const VARIANTS: Variant[] = [
  {}, // default: yellow hard hat, tan skin, blue shirt
  { h: '#f97316', i: '#c2560c', T: '#e05a4e', t: '#f0837a', u: '#b03c33' }, // orange hat, red shirt
  { H: '#8d5a3c', S: '#734125', N: '#7a4d33', A: '#8d5a3c', h: '#22c55e', i: '#15803d', T: '#334155', t: '#475569', u: '#1e293b' }, // deep skin, green hat, slate shirt
  { r: '#6b3410', H: '#e8b98f', S: '#cf9668', N: '#d6a077', A: '#e8b98f', h: '#e11d48', i: '#9f1239', T: '#a855f7', t: '#c084fc', u: '#7e22ce' }, // auburn hair, pink hat, purple shirt
  { H: '#c98a5e', S: '#a86a41', N: '#b0764a', A: '#c98a5e', h: '#0ea5e9', i: '#0369a1', T: '#f59e0b', t: '#fbbf24', u: '#b45309' }, // cyan hat, amber shirt
  { r: '#1c1917', h: '#facc15', i: '#ca8a04', T: '#14b8a6', t: '#2dd4bf', u: '#0f766e' }, // teal shirt
  { H: '#6f4426', S: '#57331a', N: '#5f3a21', A: '#6f4426', r: '#0c0a09', h: '#94a3b8', i: '#64748b', T: '#6366f1', t: '#818cf8', u: '#4338ca' }, // dark skin, gray hat, indigo shirt
]

export const GRID_W = BASE_ROWS[0].length
export const GRID_H = BASE_ROWS.length

export type Frame = { rows: string[]; palette: Record<string, string> }

// Merge a small overlay of [col,row,char] cells onto the base rows, layering
// extraPalette on top of BASE_PALETTE. Lets every action share one body.
function frame(overlay: Array<[number, number, string]>, extraPalette: Record<string, string> = {}): Frame {
  const rows = BASE_ROWS.map((r) => r.split(''))
  for (const [x, y, ch] of overlay) rows[y][x] = ch
  return { rows: rows.map((r) => r.join('')), palette: { ...BASE_PALETTE, ...extraPalette } }
}

const ARM = { a: '#f0a875' } // acting-arm recolor (skin) — retinted per variant

// 2-frame walk cycle. Base stance has both boots flat at row 22 (cols 3-6 and
// 9-12) with soles at 23. A stride lifts one boot up a row and extends the
// other outward, clearing the lifted foot's old sole cell — a visible swing,
// not a same-char no-op. '.' clears a cell back to transparent.
const WALK_LEGS: Array<Array<[number, number, string]>> = [
  // left foot forward/down, right foot lifted+back
  [[2, 22, 'B'], [3, 22, 'B'], [2, 23, 'o'], [3, 23, 'o'], // left boot extends outward
   [11, 22, '.'], [12, 22, '.'], [11, 23, '.'], [12, 23, '.'], [10, 21, 'B'], [11, 21, 'B']], // right boot lifts up
  // right foot forward/down, left foot lifted+back
  [[12, 22, 'B'], [13, 22, 'B'], [12, 23, 'o'], [13, 23, 'o'], // right boot extends outward
   [3, 22, '.'], [4, 22, '.'], [3, 23, '.'], [4, 23, '.'], [4, 21, 'B'], [5, 21, 'B']], // left boot lifts up
]

// Right arm columns are 12-13; hand/glove row 16. Tools are held out from the
// right hand. Coordinates re-authored for the 16x24 grid.
const ACTION_FRAMES: Record<Exclude<Action, 'robot'>, Frame[]> = {
  idle: [
    frame([]),
    frame([[7, 10, 'N'], [8, 10, 'N']]), // breathing: neck widens
    frame([]),
    frame([[5, 6, 'H'], [10, 6, 'H'], [6, 6, 'e'], [11, 6, 'e']]), // eyes glance aside
  ],
  drawing: [
    // pencil scribbles a left-right arc at hip height, hand following
    frame([[12, 13, 'a'], [13, 14, 'a'], [13, 15, 'c'], [13, 16, 'c']], { ...ARM, c: '#fde68a' }),
    frame([[12, 13, 'a'], [11, 14, 'a'], [10, 15, 'c'], [10, 16, 'c']], { ...ARM, c: '#fde68a' }),
    frame([[12, 13, 'a'], [9, 14, 'a'], [8, 15, 'c'], [8, 16, 'c']], { ...ARM, c: '#fde68a' }),
    frame([[12, 13, 'a'], [11, 14, 'a'], [10, 15, 'c'], [10, 16, 'c']], { ...ARM, c: '#fde68a' }),
  ],
  inspecting: [
    // magnifying glass (2x2 lens + handle) sweeps at head height
    frame([[12, 11, 'a'], [13, 9, 'a'], [12, 6, 'g'], [13, 6, 'g'], [12, 7, 'g'], [13, 7, 'g'], [13, 8, 'g']], { ...ARM, g: '#e2e8f0' }),
    frame([[12, 11, 'a'], [10, 9, 'a'], [8, 6, 'g'], [9, 6, 'g'], [8, 7, 'g'], [9, 7, 'g'], [9, 8, 'g']], { ...ARM, g: '#e2e8f0' }),
    frame([[12, 11, 'a'], [13, 9, 'a'], [12, 6, 'g'], [13, 6, 'g'], [12, 7, 'g'], [13, 7, 'g'], [13, 8, 'g']], { ...ARM, g: '#e2e8f0' }),
  ],
  hammering: [
    // hammer (2x2 head + handle) arcs overhead -> mid -> strike at hip -> up
    frame([[12, 8, 'a'], [13, 6, 'a'], [12, 3, 'm'], [13, 3, 'm'], [12, 4, 'm'], [13, 4, 'm'], [13, 5, 'a']], { ...ARM, m: '#d6d3d1' }),
    frame([[12, 11, 'a'], [13, 11, 'a'], [12, 12, 'm'], [13, 12, 'm'], [12, 13, 'm'], [13, 13, 'm']], { ...ARM, m: '#d6d3d1' }),
    frame([[12, 14, 'a'], [13, 14, 'a'], [12, 15, 'm'], [13, 15, 'm'], [12, 16, 'm'], [13, 16, 'm']], { ...ARM, m: '#d6d3d1' }),
    frame([[12, 11, 'a'], [13, 11, 'a'], [12, 12, 'm'], [13, 12, 'm'], [12, 13, 'm'], [13, 13, 'm']], { ...ARM, m: '#d6d3d1' }),
  ],
  testing: [
    // flask (2x2 body + neck, teal liquid) tips and shakes at chest height
    frame([[12, 11, 'a'], [13, 12, 'a'], [12, 8, 'f'], [13, 8, 'f'], [12, 9, 'f'], [13, 9, 'f'], [13, 7, 'f']], { ...ARM, f: '#2dd4bf' }),
    frame([[12, 10, 'a'], [13, 11, 'a'], [11, 7, 'f'], [12, 7, 'f'], [11, 8, 'f'], [12, 8, 'f'], [12, 6, 'f']], { ...ARM, f: '#2dd4bf' }),
    frame([[12, 11, 'a'], [13, 10, 'a'], [13, 6, 'f'], [13, 7, 'f'], [12, 7, 'f'], [12, 8, 'f'], [13, 8, 'f']], { ...ARM, f: '#2dd4bf' }),
    frame([[12, 10, 'a'], [13, 11, 'a'], [11, 7, 'f'], [12, 7, 'f'], [11, 8, 'f'], [12, 8, 'f'], [12, 6, 'f']], { ...ARM, f: '#2dd4bf' }),
  ],
  approving: [
    // stamp raised overhead, then slams onto a document at hip level
    frame([[12, 8, 'a'], [13, 6, 'a'], [12, 3, 'p'], [13, 3, 'p'], [12, 4, 'p'], [13, 4, 'p'], [13, 5, 'p']], { ...ARM, p: '#f472b6' }),
    frame([[12, 12, 'a'], [13, 12, 'a'], [12, 13, 'p'], [13, 13, 'p'], [12, 14, 'p'], [13, 14, 'p']], { ...ARM, p: '#f472b6' }),
    frame([[12, 12, 'a'], [13, 12, 'a'], [12, 13, 'p'], [13, 13, 'p'], [12, 14, 'p'], [13, 14, 'p']], { ...ARM, p: '#f472b6' }),
  ],
  celebrating: [
    // both arms swing from down-at-sides up into a raised "V" and back
    frame([[13, 11, 'a'], [13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a'], [2, 11, 'a'], [2, 10, 'a'], [2, 9, 'a'], [2, 8, 'a']], ARM),
    frame([[13, 11, 'a'], [13, 12, 'a'], [13, 13, 'a'], [13, 14, 'a'], [2, 11, 'a'], [2, 12, 'a'], [2, 13, 'a'], [2, 14, 'a']], ARM),
    frame([[13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a'], [13, 6, 'a'], [2, 10, 'a'], [2, 9, 'a'], [2, 8, 'a'], [2, 6, 'a']], ARM),
    frame([[13, 11, 'a'], [13, 12, 'a'], [13, 13, 'a'], [13, 14, 'a'], [2, 11, 'a'], [2, 12, 'a'], [2, 13, 'a'], [2, 14, 'a']], ARM),
  ],
  waving: [
    // right arm sweeps from resting at the side up to a raised wave and back
    frame([[13, 11, 'a'], [13, 12, 'a'], [13, 13, 'a'], [13, 14, 'a']], ARM),
    frame([[13, 11, 'a'], [13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a']], ARM),
    frame([[13, 10, 'a'], [13, 9, 'a'], [13, 7, 'a'], [13, 6, 'a']], ARM),
    frame([[13, 11, 'a'], [13, 10, 'a'], [13, 9, 'a'], [13, 8, 'a']], ARM),
  ],
}

// Distinct robot sprite on the same 16x24 grid: boxy visored head, antenna,
// segmented arms, tread base. Chars: a antenna, n antenna-stalk, r frame,
// V visor, R body, M arm, j joint, d tread, D tread-shade, w wheel.
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
  // A scan band: dim two visor rows so the "eye" reads as scanning.
  const rows = ROBOT_ROWS_BASE.map((r) => r.split(''))
  for (let x = 0; x < GRID_W; x++) {
    if (rows[5][x] === 'V') rows[5][x] = 'v'
    if (rows[6][x] === 'V') rows[6][x] = 'v'
  }
  return { rows: rows.map((r) => r.join('')), palette: { ...ROBOT_PALETTE, v: visorFill, a: antennaFill } }
}
const ROBOT_FRAMES: Frame[] = [
  robotFrame('#312e81', '#a5b4fc'), // scan band low, antenna dim
  robotFrame('#e0e7ff', '#f472b6'), // scan band lit, antenna blinks bright
  robotFrame('#312e81', '#a5b4fc'),
]

export function actionFrameCount(action: Action): number {
  return action === 'robot' ? ROBOT_FRAMES.length : ACTION_FRAMES[action].length
}

// Apply a per-worker variant recolor to a frame's palette. Skips robots
// (their palette has no variant keys, so this is a no-op for them). The
// acting-arm char 'a' tracks the variant's arm-skin so tool-holding arms
// match the body.
function applyVariant(f: Frame, variant?: Variant): Frame {
  if (!variant) return f
  const palette: Record<string, string> = { ...f.palette, ...variant }
  if (variant.A && palette.a) palette.a = variant.A // keep acting arm in sync
  return { rows: f.rows, palette }
}

// Compose the sprite for a given action + action-frame, optionally overlaying
// a walk-cycle leg pose (walkLeg >= 0) for a moving humanoid and a per-worker
// variant recolor. Robots roll on treads and ignore walkLeg + variant.
export function composeFrame(action: Action, frameIndex: number, walkLeg = -1, variant?: Variant): Frame {
  if (action === 'robot') {
    return ROBOT_FRAMES[frameIndex % ROBOT_FRAMES.length]
  }
  const frames = ACTION_FRAMES[action]
  const base = frames[frameIndex % frames.length]
  let composed = base
  if (walkLeg >= 0) {
    const rows = base.rows.map((r) => r.split(''))
    for (const [x, y, ch] of WALK_LEGS[walkLeg % WALK_LEGS.length]) rows[y][x] = ch
    composed = { rows: rows.map((r) => r.join('')), palette: base.palette }
  }
  return applyVariant(composed, variant)
}
