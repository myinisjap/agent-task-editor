import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// Static guard for the light-theme contrast rule (see AGENTS.md "Theming"):
// `text-white` must not sit on slate/neutral fills or on -700/-800/-900 fills,
// because those fills flip to pastels in light mode while `white` does not.
// Complements e2e/contrast.spec.ts, which can't reach states absent from a
// fresh stack (e.g. Inbox action buttons).

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (p.endsWith('.tsx') && !/\.test\.tsx$/.test(p)) out.push(p)
  }
  return out
}

const BAD_FILL = /\bbg-(?:(?:slate|gray|zinc|neutral|stone)-(?:[1-9]00|950)|[a-z]+-(?:700|800|900))\b/
// slate-600 is not remapped by the light theme, so white on it is fine.
const OK_FILL = /\bbg-(?:slate|gray|zinc|neutral|stone)-600\b/

describe('text-white placement', () => {
  it('is never combined with a flipping fill in one string', () => {
    const offenders: string[] = []
    for (const file of walk(join(__dirname, '..'))) {
      readFileSync(file, 'utf8').split('\n').forEach((line, i) => {
        const literals = line.match(/(['"`])(?:(?!\1).)*\1/g) ?? []
        for (const lit of literals) {
          if (lit.startsWith('`') && lit.includes('${')) continue // inner quoted pieces are checked on their own
          if (!/(?<![\w:-])text-white\b/.test(lit)) continue
          const stripped = lit.replace(new RegExp(OK_FILL, 'g'), '')
          if (BAD_FILL.test(stripped)) offenders.push(`${file}:${i + 1}: ${lit.slice(0, 120)}`)
        }
      })
    }
    expect(offenders).toEqual([])
  })
})
