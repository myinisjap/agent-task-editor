import { defineConfig, devices } from '@playwright/test'

// E2E smoke tests run against the built docker-compose stack
// (`docker compose up -d --build --wait` from the repo root), not a
// Playwright-managed dev server — this is a multi-container stack
// (nginx-served frontend + Go backend + sqlite), so there's no single
// `webServer` command to hand Playwright. Start the stack yourself
// (`./dev.sh start` or `docker compose up -d --build --wait`) before
// running `npm run e2e`. See e2e/README.md.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  globalSetup: './e2e/global-setup.ts',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5173/tasks/',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      // Runs every spec again under a mobile viewport (touch, ~412px width,
      // Android UA) so pages that render a different mobile layout (e.g. the
      // collapsed nav sidebar, ChatPage/AgentConfigPage/ProviderConfigPage's
      // mobile-only header bars) are exercised too. `npm run e2e` runs both
      // projects — no separate CI job/invocation needed.
      name: 'mobile-chrome',
      use: { ...devices['Pixel 7'] },
    },
  ],
})
