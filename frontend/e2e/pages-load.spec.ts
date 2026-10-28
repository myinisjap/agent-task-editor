import { test, expect, type Locator, type Page } from '@playwright/test'

// Data-driven smoke coverage: every static route in the app (see
// src/App.tsx's AppRoutes) is asserted to load correctly — a real,
// page-specific element renders — on whichever viewport/project Playwright
// is currently running (see playwright.config.ts: 'chromium' = desktop,
// 'mobile-chrome' = mobile). `tasks/:id` needs a real task id and is
// already exercised end-to-end by board.spec.ts, so it's intentionally
// excluded here.
//
// These are load assertions only: they confirm the route's lazy chunk
// mounts and its page-level anchor becomes visible. They deliberately do
// NOT assert on list/empty-state content (config pages may have no data on
// a fresh stack) or on HealthPage's provider-check results (no LLM
// credentials in CI, so checks are expected to show warnings/errors).
// Home is the index route; it lands on the Board until first-run setup is
// complete, so these entries dismiss the checklist first.
const dismissOnboarding = () => localStorage.setItem('board.onboarding.dismissed', 'true')

const pages: { path: string; name: string; anchor: (page: Page) => Locator; init?: () => void }[] = [
  {
    path: '',
    name: 'Home',
    anchor: (page) => page.getByRole('heading', { name: 'Home', exact: true }),
    init: dismissOnboarding,
  },
  {
    path: 'dashboard/usage',
    name: 'Cost & Usage',
    anchor: (page) => page.getByRole('heading', { name: 'Cost & Usage', exact: true }),
  },
  {
    path: 'dashboard/performance',
    name: 'Agent Performance',
    anchor: (page) => page.getByRole('heading', { name: 'Agent Performance', exact: true }),
  },
  {
    path: 'inbox',
    name: 'Inbox',
    anchor: (page) => page.getByRole('heading', { name: 'Inbox', exact: true }),
  },
  {
    path: 'board',
    name: 'Board',
    anchor: (page) => page.getByRole('button', { name: 'About the board' }),
  },
  {
    path: 'chat',
    name: 'Chat',
    anchor: (page) => page.getByTestId('chat-page'),
  },
  {
    path: 'pipeline/overview',
    name: 'Pipeline Overview',
    anchor: (page) => page.getByTestId('pipeline-overview'),
  },
  {
    path: 'pipeline/workflow',
    name: 'Workflow',
    anchor: (page) => page.getByTestId('workflow-page'),
  },
  {
    path: 'pipeline/agents',
    name: 'Agents',
    anchor: (page) => page.getByTestId('agents-page'),
  },
  {
    path: 'settings/providers',
    name: 'Providers',
    anchor: (page) => page.getByTestId('providers-page'),
  },
  {
    path: 'settings/pricing',
    name: 'Model Pricing',
    anchor: (page) => page.getByRole('heading', { name: 'Model Pricing', exact: true }),
  },
  {
    path: 'repos',
    name: 'Repos',
    anchor: (page) => page.getByRole('heading', { name: 'Repos', exact: true }),
  },
  {
    path: 'settings/intake-rules',
    name: 'Intake Rules',
    anchor: (page) => page.getByRole('heading', { name: 'Intake Routing Rules', exact: true }),
  },
  {
    path: 'settings/templates',
    name: 'Task Templates',
    anchor: (page) => page.getByRole('heading', { name: 'Task Templates', exact: true }),
  },
  {
    path: 'settings/health',
    name: 'Provider Health',
    anchor: (page) => page.getByRole('heading', { name: 'Provider Health', exact: true }),
  },
]

// Retired top-level routes redirect to their new tabbed homes (bookmarks).
const redirects: { from: string; to: RegExp }[] = [
  { from: 'workflow', to: /\/pipeline\/workflow$/ },
  { from: 'agents', to: /\/pipeline\/agents$/ },
  { from: 'providers', to: /\/settings\/providers$/ },
  { from: 'pricing', to: /\/settings\/pricing$/ },
  { from: 'templates', to: /\/settings\/templates$/ },
  { from: 'intake-rules', to: /\/settings\/intake-rules$/ },
  { from: 'health', to: /\/settings\/health$/ },
  { from: 'pipeline', to: /\/pipeline\/overview$/ },
  { from: 'settings', to: /\/settings\/providers$/ },
]

// /dashboard (the old Overview) now redirects to Home at `/`, which itself
// lands on the Board until setup completes.
test('/dashboard redirects to Home', async ({ page }) => {
  await page.goto('dashboard')
  await expect(page).toHaveURL(/\/tasks\/(board)?$/)
})

test.describe('legacy routes redirect', () => {
  for (const { from, to } of redirects) {
    test(`/${from} redirects`, async ({ page }) => {
      await page.goto(from)
      await expect(page).toHaveURL(to)
    })
  }
})

test.describe('all pages load', () => {
  for (const { path, name, anchor, init } of pages) {
    test(`${name} page loads`, async ({ page }) => {
      if (init) await page.addInitScript(init)
      await page.goto(path)
      await expect(anchor(page)).toBeVisible()
      // No horizontal scroll (matters on the mobile project).
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      if (name === 'Home') expect(overflow).toBeLessThanOrEqual(0)
    })
  }
})

// The built app is served under /tasks/; nginx redirects the bare root (the
// documented start URL) there. Regression test for a blank page at `/`.
// The app's index route then lands on the Board until first-run setup is
// complete (the e2e stack has no agents configured), otherwise Home.
test('root URL redirects to the app under /tasks/', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveURL(/\/tasks\/(board)?$/)
  await expect(
    page
      .getByRole('heading', { name: 'Home', exact: true })
      .or(page.getByRole('button', { name: 'About the board' })),
  ).toBeVisible()
})

test('Home fits a 1080p viewport without page scroll when the scene is expanded', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 })
  await page.addInitScript(() => {
    localStorage.setItem('board.onboarding.dismissed', 'true')
    localStorage.setItem('home.scene.expanded.wide', '1')
  })
  await page.goto('')
  await expect(page.getByRole('heading', { name: 'Home', exact: true })).toBeVisible()
  await page.waitForTimeout(500)
  const overflow = await page.evaluate(() => document.documentElement.scrollHeight - window.innerHeight)
  expect(overflow).toBeLessThanOrEqual(0)
})
