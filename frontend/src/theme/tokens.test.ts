import { describe, expect, it } from 'vitest'

// Guards the WCAG AA contrast of the semantic tokens in index.css (ui/README.md).
const sources = import.meta.glob('../index.css', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
const css = Object.values(sources)[0]

function parse(block: string): Record<string, [number, number, number]> {
  const out: Record<string, [number, number, number]> = {}
  for (const m of block.matchAll(/--color-([a-z-]+):\s*oklch\(([\d.]+)\s+([\d.]+)\s+([\d.]+)\)/g)) {
    if (/-\d{2,3}$/.test(m[1])) continue
    out[m[1]] = [+m[2], +m[3], +m[4]]
  }
  return out
}
const dark = parse(/@theme\s*\{([^}]*)\}/.exec(css)![1])
const lightBlocks = [...css.matchAll(/\.light\s*\{([^}]*)\}/g)].map((m) => m[1])
const light = { ...dark, ...parse(lightBlocks.find((b) => b.includes('--color-surface:'))!) }

function luminance([L, C, h]: [number, number, number]): number {
  const a = C * Math.cos((h * Math.PI) / 180)
  const b = C * Math.sin((h * Math.PI) / 180)
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  const lin = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ].map((v) => Math.min(1, Math.max(0, v)))
  return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2]
}
function ratio(a: [number, number, number], b: [number, number, number]): number {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p)
  return (x + 0.05) / (y + 0.05)
}

const SURFACES = ['surface', 'surface-raised', 'surface-sunken']
const STATUS = ['success', 'warning', 'danger', 'info', 'gate']
const pairs: [string, string][] = []
for (const fg of ['fg', 'fg-muted', 'fg-subtle', 'accent-fg', ...STATUS]) for (const s of SURFACES) pairs.push([fg, s])
for (const t of STATUS) { pairs.push([t, `${t}-soft`]); pairs.push(['fg', `${t}-soft`]) }
pairs.push(['on-accent', 'accent'], ['on-accent', 'danger-solid'])

describe('semantic token contrast', () => {
  it('parses tokens', () => {
    expect(Object.keys(dark).length).toBeGreaterThanOrEqual(19)
    expect(light.surface).not.toEqual(dark.surface)
  })
  for (const [name, theme] of [['dark', dark], ['light', light]] as const) {
    it.each(pairs)(`${name}: %s on %s is >= 4.5:1`, (fg, bg) => {
      expect(theme[fg], fg).toBeDefined()
      expect(theme[bg], bg).toBeDefined()
      expect(ratio(theme[fg], theme[bg])).toBeGreaterThanOrEqual(4.5)
    })
  }
})
