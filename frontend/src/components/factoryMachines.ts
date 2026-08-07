// Vector art DATA for the "factory assembly line" dashboard visualization —
// the clean-vector counterpart to the pixel-art officeScene. Pure string
// builders, no React and no renderer: FactoryLine.tsx injects these as inline
// SVG. Each workflow label maps to a MACHINE that performs an action
// representative of that step, and the task being assembled is an ITEM that
// accretes structure (billet -> blueprinted -> bolted -> tested -> approved ->
// packed) as it rides the conveyors down the line.
//
// All art is authored in a 132x104 viewBox. The item sits around (52..80,
// 46..80) so a tight crop renders it on the belts too. Gradient/pattern ids are
// `fl`-prefixed to avoid colliding with anything else on the page.

// Colors reach this module from workflow label rows, which are user-supplied
// (and shareable via workflow YAML). The machine art below is built as raw
// HTML strings and injected via dangerouslySetInnerHTML / innerHTML by
// FactoryLine.tsx, so an unvalidated color could break out of the SVG markup
// and inject arbitrary HTML (stored XSS, see #343). The server now rejects
// non-hex colors on write, but we normalize here too so this module's only
// two entry points (machineSvg, itemSvg) are safe on their own regardless of
// caller. TODO: render this art as React elements to remove the raw-HTML
// sink entirely — tracked as the durable follow-up to #343.
const HEX_COLOR = /^#[0-9a-fA-F]{3,8}$/
export const FACTORY_FALLBACK_COLOR = '#6b7280'
export function safeColor(c: string | undefined | null): string {
  return typeof c === 'string' && HEX_COLOR.test(c) ? c : FACTORY_FALLBACK_COLOR
}

export type FactoryAction =
  | 'idle'
  | 'drawing'
  | 'inspecting'
  | 'hammering'
  | 'testing'
  | 'robot'
  | 'approving'
  | 'packing'

// Default (seeded) 8-label workflow → a bespoke machine per label.
export const DEFAULT_FACTORY_ACTIONS: Record<string, FactoryAction> = {
  not_ready: 'idle',
  plan: 'drawing',
  'review-plan': 'inspecting',
  work: 'hammering',
  testing: 'testing',
  'agent-review': 'robot',
  review: 'approving',
  done: 'packing',
}

// Custom workflows collapse to 3 buckets (see taskBuckets.ts) → 3 machines.
export const BUCKET_FACTORY_ACTIONS: Record<'notReady' | 'agentWorking' | 'waitingHuman', FactoryAction> = {
  notReady: 'idle',
  agentWorking: 'robot',
  waitingHuman: 'approving',
}

// Shared gradients + hazard-stripe pattern, mounted once by the component.
export const FACTORY_DEFS = `
  <linearGradient id="flMetal" x1="0" y1="0" x2="0" y2="1">
    <stop offset="0" stop-color="#cdd7e6"/><stop offset=".5" stop-color="#a7b3c7"/><stop offset="1" stop-color="#818fa6"/>
  </linearGradient>
  <linearGradient id="flMetalDark" x1="0" y1="0" x2="0" y2="1">
    <stop offset="0" stop-color="#6d7a93"/><stop offset="1" stop-color="#3c465a"/>
  </linearGradient>
  <linearGradient id="flGloss" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0" stop-color="#ffffff" stop-opacity=".65"/><stop offset=".45" stop-color="#ffffff" stop-opacity="0"/>
  </linearGradient>
  <pattern id="flWarn" width="8" height="8" patternTransform="rotate(45)" patternUnits="userSpaceOnUse">
    <rect width="8" height="8" fill="#f59e0b"/><rect width="4" height="8" fill="#111827"/>
  </pattern>
`

const VB = 'viewBox="0 0 132 104"'

function bolt(x: number, y: number): string {
  return `<circle cx="${x}" cy="${y}" r="2" fill="#8794a8"/>` +
         `<circle cx="${x - 0.5}" cy="${y - 0.5}" r=".8" fill="#cbd5e1"/>`
}

function rollers(x: number, y: number, n: number): string {
  let s = ''
  for (let i = 0; i < n; i++) {
    s += `<circle cx="${x + i * 10}" cy="${y}" r="4" fill="#2c3a55" stroke="#3a4a67"/>` +
         `<circle cx="${x + i * 10 - 1}" cy="${y - 1}" r="1" fill="#5b6b86"/>`
  }
  return s
}

// The part being assembled. `stage` (0..6, derived from the station's position
// on the line) controls how much structure it has gained.
export function itemSvg(stage: number, c: string): string {
  c = safeColor(c)
  let g = '<ellipse cx="66" cy="80" rx="17" ry="3" fill="#000" opacity=".35"/>'
  g += '<rect x="52" y="54" width="28" height="23" rx="3" fill="url(#flMetal)" stroke="#5b6b86" stroke-width="1"/>'
  g += '<rect x="52" y="54" width="28" height="5" rx="3" fill="#e4ebf5"/>'
  g += '<rect x="52" y="72" width="28" height="5" rx="3" fill="#6d7a93" opacity=".55"/>'
  if (stage >= 1) {
    g += `<g stroke="${c}" stroke-width="1" opacity=".7" fill="none">` +
         '<line x1="55" y1="62" x2="77" y2="62"/><line x1="55" y1="66" x2="71" y2="66"/>' +
         '<rect x="61" y="58" width="10" height="14" stroke-dasharray="2 2"/></g>'
  }
  if (stage >= 2) {
    g += '<g stroke="#7dd3fc" stroke-width="1"><line x1="53" y1="52" x2="53" y2="48"/>' +
         '<line x1="66" y1="52" x2="66" y2="47"/><line x1="79" y1="52" x2="79" y2="48"/></g>' +
         '<line x1="53" y1="49" x2="79" y2="49" stroke="#7dd3fc" stroke-width=".8" opacity=".7"/>'
  }
  if (stage >= 3) {
    g += '<rect x="58" y="47" width="16" height="9" rx="2" fill="url(#flMetalDark)" stroke="#46587a"/>' +
         '<rect x="58" y="47" width="16" height="2" fill="#8794a8" opacity=".7"/>' +
         '<line x1="66" y1="56" x2="66" y2="77" stroke="#46587a" stroke-width="1"/>' +
         bolt(56, 61) + bolt(76, 61) + bolt(56, 71) + bolt(76, 71)
  }
  if (stage >= 4) {
    g += '<rect x="57" y="62" width="13" height="9" rx="1" fill="#06121f" stroke="#0e2236"/>' +
         `<rect x="59" y="64" width="9" height="1.6" fill="${c}"/>` +
         '<rect x="59" y="67" width="5" height="1.6" fill="#34d399"/>' +
         '<path d="M70 66 q5 0 5 -6" fill="none" stroke="#46587a" stroke-width="1"/>' +
         '<circle cx="75" cy="66" r="1.3" fill="#34d399"/><circle cx="75" cy="70" r="1.3" fill="#fbbf24"/>'
  }
  if (stage >= 5) {
    g += `<circle cx="76" cy="51" r="5.5" fill="#0b1120" stroke="${c}" stroke-width="1.5"/>` +
         `<path d="M73.3 51 l2 2 l3.6 -4.2" fill="none" stroke="${c}" stroke-width="1.6" stroke-linecap="round"/>`
  }
  if (stage >= 6) {
    g += '<rect x="52" y="54" width="28" height="23" rx="3" fill="url(#flGloss)" opacity=".55"/>' +
         '<circle cx="66" cy="65" r="4" fill="#fbbf24" stroke="#b45309" stroke-width="1"/>' +
         '<circle cx="64.5" cy="63.5" r="1.2" fill="#fef3c7"/>'
  }
  return g
}

const machines: Record<FactoryAction, (c: string) => string> = {
  idle: (c) => `<svg ${VB}>
    <!-- INTAKE: a silo hopper feeds raw billets onto a roller pad -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    <rect x="40" y="6" width="52" height="26" rx="3" fill="#1b2740" stroke="#2c3a55"/>
    <rect x="40" y="6" width="52" height="6" rx="3" fill="#26344d"/>
    <rect x="47" y="14" width="7" height="12" fill="#0e1a2e"/><rect x="57" y="14" width="7" height="12" fill="#0e1a2e"/>
    <path d="M46 32 H86 L74 46 H58 Z" fill="#223049" stroke="#2c3a55"/>
    <rect x="63" y="46" width="6" height="6" fill="#1b2740"/>
    ${bolt(44, 10)}${bolt(88, 10)}${bolt(44, 28)}${bolt(88, 28)}
    <rect x="96" y="44" width="4" height="44" fill="#26344d"/>
    <circle cx="98" cy="40" r="4" fill="#0b1120" stroke="#2c3a55"/>
    <circle cx="98" cy="40" r="1.8" fill="${c}" class="fl-anim" style="animation:fl-blink 1.6s infinite"/>
    ${rollers(50, 84, 8)}
    <g class="fl-anim" style="animation:fl-bob 2.4s ease-in-out infinite">${itemSvg(0, c)}</g>
    <path d="M54 40 l6 6 l6 -6" fill="none" stroke="${c}" stroke-width="2" opacity=".4" class="fl-anim" style="animation:fl-blink 1.8s infinite"/>
  </svg>`,

  drawing: (c) => `<svg ${VB}>
    <!-- CAD DRAFTING: a centered gantry inks the blueprint onto the part -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    <rect x="8" y="10" width="34" height="30" rx="2" fill="#0b2038" stroke="#12405c"/>
    <g stroke="#1e4e6b" stroke-width="1" opacity=".8">
      <line x1="16" y1="10" x2="16" y2="40"/><line x1="24" y1="10" x2="24" y2="40"/><line x1="32" y1="10" x2="32" y2="40"/>
      <line x1="8" y1="18" x2="42" y2="18"/><line x1="8" y1="26" x2="42" y2="26"/><line x1="8" y1="34" x2="42" y2="34"/>
    </g>
    <path d="M14 34 h20 v-14 h-12 v7" fill="none" stroke="${c}" stroke-width="1.5"
          stroke-dasharray="60" stroke-dashoffset="60" class="fl-anim" style="animation:fl-drawInk 2.8s ease-in-out infinite"/>
    <rect x="44" y="20" width="52" height="6" rx="3" fill="#26344d"/><rect x="44" y="18" width="52" height="2" fill="#3a4a67"/>
    <rect x="44" y="26" width="4" height="62" fill="#1b2740"/><rect x="92" y="26" width="4" height="62" fill="#1b2740"/>
    <g class="fl-anim" style="animation:fl-sweepX 2.8s ease-in-out infinite">
      <rect x="55" y="13" width="22" height="14" rx="2" fill="#334765" stroke="#46587a"/>
      <rect x="58" y="16" width="16" height="5" rx="1" fill="#06121f"/><rect x="59" y="17" width="5" height="2" fill="${c}"/>
      ${bolt(59, 24)}${bolt(73, 24)}
      <rect x="65" y="27" width="2" height="26" fill="#46587a"/>
      <path d="M64 53 l2 5 l2 -5 Z" fill="${c}"/>
    </g>
    ${itemSvg(1, c)}
  </svg>`,

  inspecting: (c) => `<svg ${VB}>
    <!-- INSPECTION: overhead scanner + traveling magnifier + results panel -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    <rect x="22" y="8" width="60" height="12" rx="3" fill="#1b2740" stroke="#2c3a55"/>${bolt(27, 14)}${bolt(77, 14)}
    <path d="M38 20 L32 54 H76 L70 20 Z" fill="${c}" opacity=".1"/>
    <rect x="32" y="52" width="44" height="2" fill="${c}" opacity=".55" class="fl-anim" style="animation:fl-blink 1.4s infinite"/>
    <rect x="90" y="24" width="36" height="28" rx="2" fill="#06121f" stroke="#1e3a2f"/>
    <path d="M97 39 l4 4 l9 -10" fill="none" stroke="#34d399" stroke-width="3" stroke-linecap="round" class="fl-anim" style="animation:fl-blink 1.6s infinite"/>
    <rect x="95" y="46" width="26" height="2" fill="#1e3a2f"/><rect x="95" y="49" width="18" height="2" fill="#14532d"/>
    <g class="fl-anim" style="animation:fl-lensX 2.6s ease-in-out infinite">
      <rect x="52" y="20" width="4" height="16" fill="#334765"/>
      <circle cx="54" cy="44" r="12" fill="rgba(125,211,252,.08)" stroke="#7dd3fc" stroke-width="2.5"/>
      <circle cx="54" cy="44" r="6.5" fill="none" stroke="#bae6fd" stroke-width="1" opacity=".6"/>
      <rect x="61" y="52" width="4" height="13" rx="2" transform="rotate(45 63 58)" fill="#475569"/>
    </g>
    ${itemSvg(2, c)}
  </svg>`,

  hammering: (c) => `<svg ${VB}>
    <!-- ASSEMBLY PRESS: a captive hydraulic head presses down onto the part -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    <rect x="24" y="10" width="84" height="10" rx="2" fill="#26344d" stroke="#3a4a67"/>
    <rect x="26" y="20" width="10" height="66" fill="#1b2740" stroke="#2c3a55"/>
    <rect x="96" y="20" width="10" height="66" fill="#1b2740" stroke="#2c3a55"/>
    <rect x="26" y="80" width="10" height="6" fill="url(#flWarn)"/><rect x="96" y="80" width="10" height="6" fill="url(#flWarn)"/>
    ${bolt(29, 15)}${bolt(103, 15)}
    <rect x="49" y="18" width="8" height="16" rx="2" fill="#46587a" stroke="#5b6b86"/>
    <rect x="75" y="18" width="8" height="16" rx="2" fill="#46587a" stroke="#5b6b86"/>
    <rect x="51.5" y="32" width="3" height="40" fill="#3a4a67"/><rect x="77.5" y="32" width="3" height="40" fill="#3a4a67"/>
    ${itemSvg(3, c)}
    <g class="fl-anim" style="animation:fl-pressY 1.5s cubic-bezier(.5,0,.9,.4) infinite">
      <rect x="46" y="28" width="40" height="16" rx="3" fill="#3a4a67" stroke="#5b6b86"/>
      <rect x="46" y="28" width="40" height="4" fill="#5b6b86"/>
      <rect x="50" y="30" width="6" height="12" rx="1" fill="#233149"/><rect x="76" y="30" width="6" height="12" rx="1" fill="#233149"/>
      <rect x="58" y="44" width="16" height="7" rx="1" fill="#2c3a55" stroke="#5b6b86"/>
      <rect x="60" y="49" width="12" height="2.5" fill="${c}"/>
    </g>
    <g class="fl-anim" style="animation:fl-spark 1.5s infinite; transform-origin:66px 55px">
      <path d="M66 49 l3 6 l6 1 l-5 4 l2 6 l-6 -4 l-6 4 l2 -6 l-5 -4 l6 -1 Z" fill="#fde68a"/>
      <circle cx="54" cy="56" r="1.5" fill="#fbbf24"/><circle cx="78" cy="57" r="1.5" fill="#fbbf24"/>
    </g>
  </svg>`,

  testing: (c) => `<svg ${VB}>
    <!-- QA BENCH: oscilloscope waveform, gauge needle, LED bank, probes -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    <rect x="10" y="12" width="46" height="30" rx="3" fill="#06121f" stroke="#1e3a2f"/>
    <polyline points="14,32 20,32 24,22 28,38 32,26 36,32 56,32" fill="none" stroke="#34d399" stroke-width="3" opacity=".2"/>
    <polyline points="14,32 20,32 24,22 28,38 32,26 36,32 56,32" fill="none" stroke="#34d399" stroke-width="1.5"/>
    <circle cx="99" cy="26" r="16" fill="#0b1120" stroke="#2c3a55" stroke-width="2"/>
    <path d="M87 32 A16 16 0 0 1 111 32" fill="none" stroke="#334765" stroke-width="2"/>
    <g class="fl-anim" style="animation:fl-needle 1.1s ease-in-out infinite alternate; transform-origin:99px 26px">
      <rect x="98" y="12" width="2" height="16" rx="1" fill="${c}"/>
    </g>
    <circle cx="99" cy="26" r="2.5" fill="#cbd5e1"/>
    <rect x="60" y="14" width="20" height="10" rx="2" fill="#101a2e" stroke="#2c3a55"/>
    <circle cx="66" cy="19" r="2.4" fill="#34d399" class="fl-anim" style="animation:fl-led1 .9s infinite"/>
    <circle cx="74" cy="19" r="2.4" fill="#f59e0b" class="fl-anim" style="animation:fl-led2 .9s infinite"/>
    <path d="M62 44 C62 54 66 50 66 55" fill="none" stroke="#334765" stroke-width="2"/>
    <path d="M104 44 C104 54 68 52 68 55" fill="none" stroke="#334765" stroke-width="2"/>
    <circle cx="62" cy="44" r="2" fill="#ef4444"/><circle cx="104" cy="44" r="2" fill="#111827"/>
    ${itemSvg(4, c)}
  </svg>`,

  robot: (c) => `<svg ${VB}>
    <!-- AGENT SCANNER: a robot arm holds a scanner head that laser-sweeps the part -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    ${itemSvg(4, c)}
    <g class="fl-anim" style="animation:fl-coneFlicker 2s ease-in-out infinite">
      <path d="M60 49 H72 L80 77 H52 Z" fill="${c}" opacity=".12"/>
      <path d="M60 49 L52 77" stroke="${c}" stroke-width="1" opacity=".35"/>
      <path d="M72 49 L80 77" stroke="${c}" stroke-width="1" opacity=".35"/>
    </g>
    <g class="fl-anim" style="animation:fl-scanSweep 1.5s ease-in-out infinite alternate">
      <rect x="51" y="52" width="30" height="6" rx="3" fill="${c}" opacity=".3"/>
      <rect x="51" y="54" width="30" height="2" fill="${c}" opacity=".7"/>
      <rect x="52" y="54.5" width="28" height="1" fill="#e6fbff"/>
      <circle cx="52" cy="55" r="1.6" fill="#e6fbff" class="fl-anim" style="animation:fl-blink .5s infinite"/>
      <circle cx="80" cy="55" r="1.6" fill="#e6fbff" class="fl-anim" style="animation:fl-blink .5s .25s infinite"/>
    </g>
    <rect x="94" y="72" width="28" height="14" rx="3" fill="#1b2740" stroke="#2c3a55"/>
    <rect x="94" y="72" width="28" height="4" fill="#26344d"/>${bolt(99, 83)}${bolt(117, 83)}
    <circle cx="108" cy="72" r="5" fill="#334765" stroke="#46587a"/>
    <rect x="104" y="42" width="8" height="32" rx="4" fill="#3a4a67" stroke="#46587a"/>
    <rect x="106" y="44" width="2" height="28" fill="#5b6b86" opacity=".5"/>
    <rect x="62" y="38" width="48" height="8" rx="4" fill="#46587a" stroke="#5b6b86"/>
    <rect x="64" y="40" width="42" height="2" fill="#5b6b86" opacity=".5"/>
    <circle cx="108" cy="42" r="4.5" fill="#233149" stroke="#5b6b86"/>
    <rect x="56" y="33" width="18" height="15" rx="3" fill="#2c3a55" stroke="#5b6b86"/>
    <rect x="58" y="36" width="14" height="4" rx="1" fill="#06121f"/>
    <rect x="59" y="37" width="12" height="2" fill="${c}" opacity=".85"/>
    <rect x="60" y="46" width="12" height="3" rx="1.5" fill="#0b1120"/>
    <rect x="61" y="47" width="10" height="1.4" fill="#e6fbff"/>
    <circle cx="66" cy="47.5" r="2.6" fill="${c}" class="fl-anim" style="animation:fl-emit .5s ease-in-out infinite"/>
  </svg>`,

  approving: (c) => `<svg ${VB}>
    <!-- APPROVAL: a captive press stamp seals the part on top; lamp signals go -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>
    <rect x="30" y="8" width="72" height="8" rx="2" fill="#26344d"/>
    <rect x="34" y="16" width="4" height="72" fill="#1b2740"/><rect x="94" y="16" width="4" height="72" fill="#1b2740"/>
    <rect x="100" y="22" width="26" height="16" rx="3" fill="#0b1120" stroke="#2c3a55"/>
    <circle cx="108" cy="30" r="3" fill="${c}" class="fl-anim" style="animation:fl-blink 1.8s infinite"/>
    <rect x="114" y="27" width="8" height="2" fill="${c}" opacity=".6"/><rect x="114" y="31" width="8" height="2" fill="#334765"/>
    <rect x="58" y="16" width="16" height="12" rx="2" fill="#46587a" stroke="#5b6b86"/>
    <rect x="64" y="26" width="4" height="30" fill="#3a4a67"/>
    ${itemSvg(5, c)}
    <g class="fl-anim" style="animation:fl-stampY 1.9s cubic-bezier(.5,0,.9,.4) infinite">
      <rect x="62" y="30" width="8" height="10" rx="1" fill="#233149"/>
      <rect x="52" y="40" width="28" height="12" rx="3" fill="#3a4a67" stroke="#5b6b86"/>
      <rect x="52" y="40" width="28" height="3" fill="#5b6b86"/>
      <circle cx="66" cy="47" r="4" fill="none" stroke="${c}" stroke-width="1.5"/>
    </g>
    <path d="M60 68 l4 4 l9 -10" fill="none" stroke="${c}" stroke-width="3" stroke-linecap="round" class="fl-anim" style="animation:fl-stampMark 1.9s infinite"/>
  </svg>`,

  packing: (c) => `<svg ${VB}>
    <!-- PACKING: the finished unit is sealed in a labeled shipping box -->
    <rect x="6" y="88" width="120" height="4" fill="#16213a"/>${rollers(16, 86, 11)}
    <g aria-hidden="true">
      <rect x="40" y="14" width="4" height="4" fill="#f472b6" class="fl-anim" style="animation:fl-confetti 1.7s ease-in 0s infinite"/>
      <rect x="66" y="10" width="4" height="4" fill="#34d399" class="fl-anim" style="animation:fl-confetti 1.7s ease-in .5s infinite"/>
      <rect x="90" y="14" width="4" height="4" fill="#fbbf24" class="fl-anim" style="animation:fl-confetti 1.7s ease-in .9s infinite"/>
      <rect x="54" y="12" width="3" height="6" fill="#60a5fa" class="fl-anim" style="animation:fl-confetti 1.7s ease-in .3s infinite"/>
      <rect x="78" y="12" width="3" height="6" fill="#a78bfa" class="fl-anim" style="animation:fl-confetti 1.7s ease-in .7s infinite"/>
    </g>
    <rect x="44" y="48" width="44" height="34" rx="2" fill="#9a6330"/>
    <rect x="44" y="48" width="44" height="8" fill="#b0743a"/>
    <rect x="44" y="48" width="44" height="34" rx="2" fill="url(#flGloss)" opacity=".14"/>
    <rect x="62" y="48" width="8" height="34" fill="#c9a878" opacity=".85"/>
    <path d="M44 48 h22 v-13 h-22 Z" fill="#a86c34" stroke="#7c5228" class="fl-anim" style="transform-origin:44px 48px; animation:fl-flapL 2.6s ease-in-out infinite"/>
    <path d="M88 48 h-22 v-13 h22 Z" fill="#8a5a2b" stroke="#6b4a22" class="fl-anim" style="transform-origin:88px 48px; animation:fl-flapR 2.6s ease-in-out infinite"/>
    <g transform="translate(-8,-4) scale(.8)" style="transform-origin:66px 62px">${itemSvg(6, c)}</g>
    <rect x="50" y="62" width="14" height="10" rx="1" fill="#f8fafc"/>
    <rect x="52" y="64" width="10" height="1.6" fill="#94a3b8"/><rect x="52" y="67" width="7" height="1.6" fill="#94a3b8"/>
    <g stroke="#0f172a" stroke-width="1"><line x1="52" y1="69.5" x2="52" y2="71"/><line x1="54" y1="69.5" x2="54" y2="71"/><line x1="57" y1="69.5" x2="57" y2="71"/><line x1="60" y1="69.5" x2="60" y2="71"/></g>
  </svg>`,
}

export function machineSvg(action: FactoryAction, color: string): string {
  return (machines[action] ?? machines.idle)(safeColor(color))
}
