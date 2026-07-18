// Pixel-art sprite DATA for the office-floor dashboard visualization. Pure
// data + a frame-composition helper, no React and no renderer — consumed by
// officeScene.ts (canvas blitting) and covered by TaskFactory.test.tsx.
//
// Each sprite is a 12x18 grid of characters; a palette maps each char to a
// color (space and '.' are transparent). Every humanoid action shares one
// authored BASE body and overlays only the acting limb + tool per frame.

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

// Shared humanoid base (12 wide x 18 tall): hard hat, head with eyes, neck,
// torso with two full-length arms + hands, hips, two legs, boots. Per-action
// frames override only the acting limb + tool. Eyes (row 3) are literal '.'
// (transparent) which reads as two dark dots on the dark floor.
const BASE_ROWS = [
  '....hhhh....', // 0  hard-hat crown
  '...hhhhhh...', // 1  hard-hat brim
  '....HHHH....', // 2  forehead
  '....H..H....', // 3  eyes (transparent gap on skin)
  '....HHHH....', // 4  jaw/chin
  '.....NN.....', // 5  neck
  'AA.TTTTTT.AA', // 6  shoulders: arm + torso + arm
  'AA.TTTTTT.AA', // 7  upper arm + torso
  'AA.TTTTTT.AA', // 8  upper arm + torso
  'AA.TTTTTT.AA', // 9  forearm + torso
  'HA.TTTTTT.AH', // 10 hand (skin) + torso bottom
  '...PPPPPP...', // 11 belt/hips
  '...PP..PP...', // 12 hip taper (2-col leg gap)
  '...LL..LL...', // 13 upper leg
  '...LL..LL...', // 14 upper leg
  '...LL..LL...', // 15 lower leg
  '..BBB..BBB..', // 16 boots
  '..BBB..BBB..', // 17 boots
]

const BASE_PALETTE: Record<string, string> = {
  h: '#facc15', // hard hat
  H: '#f0a875', // skin
  N: '#d99361', // neck (shaded skin)
  T: '#64748b', // torso/overalls
  A: '#f0a875', // bare arm (skin)
  P: '#475569', // hips/belt
  L: '#334155', // legs
  B: '#1e293b', // boots
}

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

const ARM = { a: '#f0a875' } // acting-arm recolor (skin)

// 2-frame walk cycle: legs alternate stride. Applied while a sprite is moving.
const WALK_LEGS: Array<Array<[number, number, string]>> = [
  [[3, 13, 'L'], [4, 14, 'L'], [7, 15, 'L'], [8, 16, 'L']], // left fwd, right back
  [[7, 13, 'L'], [8, 14, 'L'], [3, 15, 'L'], [4, 16, 'L']], // right fwd, left back
]

const ACTION_FRAMES: Record<Exclude<Action, 'robot'>, Frame[]> = {
  idle: [
    frame([]),
    frame([[5, 5, 'N'], [6, 5, 'N']]), // breathing
    frame([]),
    frame([[3, 3, 'H'], [5, 3, '.'], [6, 3, '.'], [4, 3, 'H']]), // eyes shift
  ],
  drawing: [
    frame([[10, 8, 'a'], [11, 9, 'a'], [11, 10, 't'], [11, 11, 't']], { ...ARM, t: '#fde68a' }),
    frame([[10, 8, 'a'], [9, 9, 'a'], [8, 10, 't'], [8, 11, 't']], { ...ARM, t: '#fde68a' }),
    frame([[10, 8, 'a'], [7, 9, 'a'], [6, 10, 't'], [6, 11, 't']], { ...ARM, t: '#fde68a' }),
    frame([[10, 8, 'a'], [9, 9, 'a'], [8, 10, 't'], [8, 11, 't']], { ...ARM, t: '#fde68a' }),
  ],
  inspecting: [
    frame([[10, 6, 'a'], [11, 5, 'a'], [10, 3, 'g'], [11, 3, 'g'], [10, 4, 'g'], [11, 4, 'g']], { ...ARM, g: '#e2e8f0' }),
    frame([[10, 6, 'a'], [8, 5, 'a'], [6, 3, 'g'], [7, 3, 'g'], [6, 4, 'g'], [7, 4, 'g']], { ...ARM, g: '#e2e8f0' }),
    frame([[10, 6, 'a'], [11, 5, 'a'], [10, 3, 'g'], [11, 3, 'g'], [10, 4, 'g'], [11, 4, 'g']], { ...ARM, g: '#e2e8f0' }),
  ],
  hammering: [
    frame([[10, 4, 'a'], [11, 3, 'a'], [10, 0, 'm'], [11, 0, 'm'], [10, 1, 'm'], [11, 1, 'm']], { ...ARM, m: '#d6d3d1' }),
    frame([[10, 7, 'a'], [11, 7, 'a'], [10, 8, 'm'], [11, 8, 'm'], [10, 9, 'm'], [11, 9, 'm']], { ...ARM, m: '#d6d3d1' }),
    frame([[10, 10, 'a'], [11, 10, 'a'], [10, 11, 'm'], [11, 11, 'm'], [10, 12, 'm'], [11, 12, 'm']], { ...ARM, m: '#d6d3d1' }),
    frame([[10, 7, 'a'], [11, 7, 'a'], [10, 8, 'm'], [11, 8, 'm'], [10, 9, 'm'], [11, 9, 'm']], { ...ARM, m: '#d6d3d1' }),
  ],
  testing: [
    frame([[10, 7, 'a'], [11, 8, 'a'], [10, 5, 'f'], [11, 5, 'f'], [10, 6, 'f'], [11, 6, 'f']], { ...ARM, f: '#2dd4bf' }),
    frame([[10, 6, 'a'], [11, 7, 'a'], [9, 4, 'f'], [10, 4, 'f'], [9, 5, 'f'], [10, 5, 'f']], { ...ARM, f: '#2dd4bf' }),
    frame([[10, 7, 'a'], [11, 6, 'a'], [11, 3, 'f'], [11, 4, 'f'], [10, 4, 'f'], [10, 5, 'f']], { ...ARM, f: '#2dd4bf' }),
    frame([[10, 6, 'a'], [11, 7, 'a'], [9, 4, 'f'], [10, 4, 'f'], [9, 5, 'f'], [10, 5, 'f']], { ...ARM, f: '#2dd4bf' }),
  ],
  approving: [
    frame([[10, 4, 'a'], [11, 3, 'a'], [10, 0, 'p'], [11, 0, 'p'], [10, 1, 'p'], [11, 1, 'p']], { ...ARM, p: '#f472b6' }),
    frame([[10, 8, 'a'], [11, 8, 'a'], [10, 9, 'p'], [11, 9, 'p'], [10, 10, 'p'], [11, 10, 'p']], { ...ARM, p: '#f472b6' }),
    frame([[10, 8, 'a'], [11, 8, 'a'], [10, 9, 'p'], [11, 9, 'p'], [10, 10, 'p'], [11, 10, 'p']], { ...ARM, p: '#f472b6' }),
  ],
  celebrating: [
    frame([[10, 6, 'a'], [10, 5, 'a'], [10, 4, 'a'], [10, 3, 'a'], [1, 6, 'a'], [1, 5, 'a'], [1, 4, 'a'], [1, 3, 'a']], ARM),
    frame([[10, 6, 'a'], [10, 7, 'a'], [10, 8, 'a'], [10, 9, 'a'], [1, 6, 'a'], [1, 7, 'a'], [1, 8, 'a'], [1, 9, 'a']], ARM),
    frame([[11, 5, 'a'], [11, 4, 'a'], [11, 3, 'a'], [11, 2, 'a'], [0, 5, 'a'], [0, 4, 'a'], [0, 3, 'a'], [0, 2, 'a']], ARM),
    frame([[10, 6, 'a'], [10, 7, 'a'], [10, 8, 'a'], [10, 9, 'a'], [1, 6, 'a'], [1, 7, 'a'], [1, 8, 'a'], [1, 9, 'a']], ARM),
  ],
  waving: [
    frame([[10, 6, 'a'], [10, 7, 'a'], [10, 8, 'a'], [10, 9, 'a']], ARM),
    frame([[10, 6, 'a'], [10, 5, 'a'], [10, 4, 'a'], [10, 3, 'a']], ARM),
    frame([[11, 5, 'a'], [11, 4, 'a'], [11, 3, 'a'], [11, 2, 'a']], ARM),
    frame([[10, 6, 'a'], [10, 5, 'a'], [10, 4, 'a'], [10, 3, 'a']], ARM),
  ],
}

// Distinct robot sprite: boxy visored head, antenna, mechanical arms, tread
// feet. Same 12x18 grid as the humanoid.
const ROBOT_ROWS_BASE = [
  '.....a......',
  '.....a......',
  '...rrrrrr...',
  '..rVVVVVVr..',
  '..rVVVVVVr..',
  '..rrrrrrrr..',
  '....RRRR....',
  '.M.RRRRRR.M.',
  'MM.RRRRRR.MM',
  'MM.RRRRRR.MM',
  '.M.RRRRRR.M.',
  '....RRRR....',
  '....dddd....',
  '...dd.dd....',
  '...dd.dd....',
  '...dd.dd....',
  '...dd.dd....',
  '..DDD.DDD...',
]
const ROBOT_PALETTE = { a: '#a5b4fc', r: '#6366f1', V: '#e0e7ff', R: '#4f46e5', M: '#818cf8', d: '#3730a3', D: '#1e1b4b' }
function robotFrame(visorFill: string, antennaFill: string): Frame {
  const rows = ROBOT_ROWS_BASE.map((r) => r.split(''))
  for (let x = 3; x <= 8; x++) {
    if (rows[3][x] === 'V') rows[3][x] = 'v'
    if (rows[4][x] === 'V') rows[4][x] = 'v'
  }
  return { rows: rows.map((r) => r.join('')), palette: { ...ROBOT_PALETTE, v: visorFill, a: antennaFill } }
}
const ROBOT_FRAMES: Frame[] = [
  robotFrame('#e0e7ff', '#a5b4fc'), // visor lit, antenna dim
  robotFrame('#312e81', '#f472b6'), // visor scan-dark, antenna blinks
  robotFrame('#e0e7ff', '#a5b4fc'),
]

export function actionFrameCount(action: Action): number {
  return action === 'robot' ? ROBOT_FRAMES.length : ACTION_FRAMES[action].length
}

// Compose the sprite for a given action + action-frame, optionally overlaying
// a walk-cycle leg pose (walkLeg >= 0) for a moving humanoid. Robots roll on
// treads, so they ignore walkLeg.
export function composeFrame(action: Action, frameIndex: number, walkLeg = -1): Frame {
  if (action === 'robot') {
    return ROBOT_FRAMES[frameIndex % ROBOT_FRAMES.length]
  }
  const frames = ACTION_FRAMES[action]
  const base = frames[frameIndex % frames.length]
  if (walkLeg < 0) return base
  const rows = base.rows.map((r) => r.split(''))
  for (const [x, y, ch] of WALK_LEGS[walkLeg % WALK_LEGS.length]) rows[y][x] = ch
  return { rows: rows.map((r) => r.join('')), palette: base.palette }
}
