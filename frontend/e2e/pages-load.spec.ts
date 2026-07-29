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
const pages: { path: string; name: string; anchor: (page: Page) => Locator }[] = [
  {
    path: '',
    name: 'Dashboard',
    anchor: (page) => page.getByRole('heading', { name: 'Overview', exact: true }),
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
    path: 'board',
    name: 'Board',
    anchor: (page) => page.getByRole('heading', { name: 'Board', exact: true }),
  },
  {
    path: 'chat',
    name: 'Chat',
    anchor: (page) => page.getByTestId('chat-page'),
  },
  {
    path: 'workflow',
    name: 'Workflow',
    anchor: (page) => page.getByTestId('workflow-page'),
  },
  {
    path: 'agents',
    name: 'Agents',
    anchor: (page) => page.getByTestId('agents-page'),
  },
  {
    path: 'providers',
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
    path: 'templates',
    name: 'Task Templates',
    anchor: (page) => page.getByRole('heading', { name: 'Task Templates', exact: true }),
  },
  {
    path: 'health',
    name: 'Provider Health',
    anchor: (page) => page.getByRole('heading', { name: 'Provider Health', exact: true }),
  },
]

test.describe('all pages load', () => {
  for (const { path, name, anchor } of pages) {
    test(`${name} page loads`, async ({ page }) => {
      await page.goto(path)
      await expect(anchor(page)).toBeVisible()
    })
  }
})
