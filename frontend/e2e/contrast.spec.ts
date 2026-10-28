import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Locator, type Page } from '@playwright/test'

// Runs axe's `color-contrast` rule over the main pages in both themes and
// fails on serious/critical violations. The rule is NOT disabled; known
// pre-existing violations that are out of scope for the light-theme fix are
// listed explicitly in ALLOWLIST below. White/near-white text on a light
// fill in light mode must be fixed, never allowlisted.

type Theme = 'dark' | 'light'

// `pattern` is matched against node.html; `fg` (lowercase hex) against axe's computed
// foreground colour. An entry matches when every field it specifies matches.
const ALLOWLIST: { pattern?: RegExp; fg?: string[]; themes?: Theme[]; reason: string }[] = [
  {
    // Tailwind 600 accents (sky/emerald/pink) give ~3.8–4.1:1 with white in
    // both themes. A palette issue, not a light-theme one; needs a follow-up.
    pattern: /\bbg-(sky|emerald|pink)-600\b[^"]*\btext-white\b|\btext-white\b[^"]*\bbg-(sky|emerald|pink)-600\b/,
    reason: 'white on 600-level accent fill is <4.5:1 in both themes (palette)',
  },
  {
    // text-slate-500 (#62748e) muted text: 3.1–4.3:1 on slate surfaces in dark and
    // 3.9–4.3:1 in light (slate-500 does not flip). Pre-existing, widespread
    // design choice; follow-up is to raise muted text to slate-400/600.
    fg: ['#62748e'],
    reason: 'text-slate-500 muted text slightly below AA (pre-existing)',
  },
  {
    // text-slate-600 (#45556c) placeholder/empty-state text on near-black.
    fg: ['#45556c'],
    themes: ['dark'],
    reason: 'text-slate-600 empty-state text below AA in dark (pre-existing)',
  },
  {
    // Status colours that map to stable 600 shades in light mode (yellow-600
    // #d08700, green-600 #00a63e, amber #e17100) or tinted alert fills.
    // Dark text on pastel, not white-on-light; follow-up palette work.
    fg: ['#d08700', '#00a63e', '#e17100', '#9e6c2d'],
    themes: ['light'],
    reason: 'warning/success status text on light surfaces is 2.7–4.0:1 (pre-existing)',
  },
  {
    // indigo-200 text on the indigo-600 selected fill (3.9:1).
    fg: ['#cac4fc'],
    themes: ['light', 'dark'],
    reason: 'indigo-100/200 text on indigo-600 toggle is 3.9:1 (pre-existing)',
  },
]

const API_BASE = process.env.E2E_API_BASE_URL ?? 'http://localhost:8080/api/v1'

let taskId = ''
const taskTitle = `E2E contrast task ${Date.now()}`

test.beforeAll(async () => {
  let repoId = process.env.E2E_REPO_ID
  if (!repoId) {
    const repos = (await (await fetch(`${API_BASE}/repos`)).json()) as Array<{ id: string; name: string }> | null
    repoId = repos?.find((r) => r.name === 'e2e-demo-repo')?.id
  }
  let workflowId = process.env.E2E_WORKFLOW_ID
  if (!workflowId) {
    const wfs = (await (await fetch(`${API_BASE}/workflows`)).json()) as Array<{ id: string }>
    workflowId = wfs[0]?.id
  }
  const res = await fetch(`${API_BASE}/tasks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: taskTitle, repo_id: repoId, workflow_id: workflowId }),
  })
  if (!res.ok) throw new Error(`create task failed: ${res.status} ${await res.text()}`)
  taskId = ((await res.json()) as { id: string }).id
})

// Home lives at `/`, which lands on the Board until setup completes; the e2e
// stack never completes setup, so dismiss the checklist to reach it.
const dismissOnboarding = () => localStorage.setItem('board.onboarding.dismissed', 'true')

const pages: { name: string; path: () => string; anchor: (p: Page) => Locator; init?: () => void }[] = [
  { name: 'Home', path: () => '', anchor: (p) => p.getByTestId('home-page'), init: dismissOnboarding },
  { name: 'Board', path: () => 'board', anchor: (p) => p.getByRole('button', { name: 'About the board' }) },
  { name: 'Task Detail', path: () => `tasks/${taskId}`, anchor: (p) => p.getByText(taskTitle).first() },
  { name: 'Pipeline Overview', path: () => 'pipeline/overview', anchor: (p) => p.getByTestId('pipeline-overview') },
  {
    name: 'Provider Health',
    path: () => 'settings/health',
    anchor: (p) => p.getByRole('heading', { name: 'Provider Health', exact: true }),
  },
]

for (const theme of ['dark', 'light'] as Theme[]) {
  test.describe(`color-contrast (${theme})`, () => {
    for (const pg of pages) {
      test(`${pg.name}`, async ({ page }) => {
        await page.emulateMedia({ reducedMotion: 'reduce' })
        await page.addInitScript((t) => localStorage.setItem('theme', t), theme)
        if (pg.init) await page.addInitScript(pg.init)
        await page.goto(pg.path())
        await expect(page.locator('html')).toHaveClass(new RegExp(`\\b${theme}\\b`))
        await expect(pg.anchor(page)).toBeVisible()

        const results = await new AxeBuilder({ page }).withRules(['color-contrast']).analyze()
        const remaining: string[] = []
        for (const v of results.violations) {
          if (v.impact !== 'serious' && v.impact !== 'critical') continue
          for (const node of v.nodes) {
            const d = (node.any[0]?.data ?? {}) as { fgColor?: string; bgColor?: string; contrastRatio?: number }
            if (
              ALLOWLIST.some(
                (a) =>
                  (a.pattern || a.fg) &&
                  (!a.themes || a.themes.includes(theme)) &&
                  (!a.pattern || a.pattern.test(node.html)) &&
                  (!a.fg || (d.fgColor !== undefined && a.fg.includes(d.fgColor.toLowerCase()))),
              )
            )
              continue
            remaining.push(
              `[${pg.name} / ${theme}] ${node.target.join(' ')} fg=${d.fgColor} bg=${d.bgColor} ratio=${d.contrastRatio}\n    ${node.html.slice(0, 200)}`,
            )
          }
        }
        expect(remaining, remaining.join('\n')).toEqual([])
      })
    }
  })
}
