/*
 * Generates the light-theme color variables embedded in src/index.css.
 *
 * The app is authored dark-first with Tailwind's default palette. The light
 * theme is a perceptual "index mirror" of that ramp: shade 50 borrows 950's
 * value, 100<->900, 200<->800, 300<->700, 400->600 and vice-versa, while the
 * mid shades (500/600) are kept near-saturated so primary buttons keep their
 * contrast with white text in both themes.
 *
 * Usage:
 *   node scripts/gen-light-theme.mjs
 * then paste the output into the `.light { … }` block of src/index.css.
 * Re-run after upgrading Tailwind to pick up palette changes.
 */
import fs from 'node:fs'

const theme = fs.readFileSync('node_modules/tailwindcss/theme.css', 'utf8')

// Remap all standard families so the theme survives new accent colors landing
// in components later.
const families = [
  'slate', 'gray', 'zinc', 'neutral', 'stone', 'red', 'orange', 'amber',
  'yellow', 'lime', 'green', 'emerald', 'teal', 'cyan', 'sky', 'blue',
  'indigo', 'violet', 'purple', 'fuchsia', 'pink', 'rose',
]

// Parse `--color-<family>-<shade>: <value>;` out of Tailwind's theme.css.
const vals = {}
const re = /--color-([a-z]+)-(\d+):\s*([^;]+);/g
let m
while ((m = re.exec(theme))) {
  const [, fam, shade, val] = m
  vals[`${fam}-${shade}`] = val.trim()
}

// Light-mode index mirror. Mid shades (500/600) stay put so button fills keep
// their contrast; the dark ends flip to light and the light ends flip to dark.
const map = { 50: 950, 100: 900, 200: 800, 300: 700, 400: 600, 500: 500, 600: 600, 700: 300, 800: 200, 900: 100, 950: 50 }
const shades = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950]

const out = []
for (const fam of families) {
  for (const s of shades) {
    const src = `${fam}-${map[s]}`
    if (!vals[src]) continue
    out.push(`  --color-${fam}-${s}: ${vals[src]};`)
  }
}
console.log(out.join('\n'))
