# E2E smoke tests (Playwright)

A small suite of end-to-end smoke tests that run against the real,
built docker-compose stack (nginx-served frontend + Go backend + sqlite) —
not against `npm run dev` and not with any mocked API calls. Kept to a
handful of high-value flows (board load, task creation, task-detail
navigation, WS log pane mount) plus a data-driven check that every app
route loads (`pages-load.spec.ts`), so it stays fast and non-flaky; this is
not a full E2E suite.

Every spec runs under two Playwright projects — `chromium` (desktop) and
`mobile-chrome` (a Pixel 7 viewport/UA, exercising the app's mobile layout:
collapsed nav sidebar, mobile header bars, etc.) — see
`playwright.config.ts`. `npm run e2e` runs both projects; there's no
separate mobile-only command.

`pages-load.spec.ts` navigates directly (by URL) to every static route
declared in `src/App.tsx` and asserts a page-specific element (a heading or
a `data-testid` page-root anchor) becomes visible. It intentionally does
NOT assert on list/empty-state content — config pages may have no data on
a fresh stack — or on `HealthPage`'s provider-check results, which are
expected to show warnings/errors since CI has no LLM credentials. The one
route it skips is `/tasks/:id`, which needs a real task id and is already
covered end-to-end by `board.spec.ts`.

## Running locally

1. Create a demo repo directory under `REPO_BASE_DIR` (defaults to
   `/tmp/repos`) so the backend has something to register as a repo:

   ```bash
   mkdir -p /tmp/repos/demo-repo
   git -C /tmp/repos/demo-repo init -q
   git -C /tmp/repos/demo-repo -c user.name=demo -c user.email=demo@example.com \
     commit -q --allow-empty -m "Initial commit"
   ```

   (This directory must exist *before* the stack starts, since
   `REPO_BASE_DIR` is bind-mounted into the backend container.)

2. Start the stack from the repo root:

   ```bash
   docker compose up -d --build --wait
   ```

3. Install Playwright's Chromium browser (first time only):

   ```bash
   cd frontend
   npm run e2e:install
   ```

4. Run the suite:

   ```bash
   npm run e2e
   ```

`e2e/global-setup.ts` registers a repo (`e2e-demo-repo`, pointing at
`/tmp/repos/demo-repo` by default — override with `E2E_DEMO_REPO_PATH`) and
links it to the backend's default workflow via the API before the tests
run, so no manual seeding through the UI is needed. It's idempotent: safe
to re-run against a stack that's still up from a previous run.

Override the frontend URL with `E2E_BASE_URL` (defaults to
`http://localhost:5173/tasks/`) and the backend API URL used by
`global-setup.ts` with `E2E_API_BASE_URL` (defaults to
`http://localhost:8080/api/v1`) if your stack is exposed elsewhere.
