import { test, expect } from '@playwright/test'

// Desktop-only smoke: at >=1280px the Inbox is two panes and selecting a row
// shows its preview and writes ?task=<id> to the URL.
const API_BASE = process.env.E2E_API_BASE_URL ?? 'http://localhost:8080/api/v1'

test.describe('inbox two-pane preview', () => {
  let taskId = ''
  const title = `E2E inbox ${Date.now()}`

  test.beforeEach(async ({ browserName: _b }, testInfo) => {
    test.skip(testInfo.project.name === 'mobile-chrome', 'desktop two-pane only')
    let repoId = process.env.E2E_REPO_ID
    if (!repoId) {
      const repos = (await (await fetch(`${API_BASE}/repos`)).json()) as Array<{ id: string; name: string }> | null
      repoId = repos?.find((r) => r.name === 'e2e-demo-repo')?.id
    }
    // `review` is a human gate in the seeded default workflow, so the task lands in Decide.
    const res = await fetch(`${API_BASE}/tasks`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title, repo_id: repoId, label: 'review' }),
    })
    if (!res.ok) throw new Error(`create task failed: ${res.status} ${await res.text()}`)
    taskId = ((await res.json()) as { id: string }).id
  })

  test.afterEach(async () => {
    if (taskId) await fetch(`${API_BASE}/tasks/${taskId}`, { method: 'DELETE' })
    taskId = ''
  })

  test('selecting a row shows the preview and keeps place in the URL', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('inbox')
    const row = page.getByTestId('inbox-item').filter({ hasText: title })
    await expect(row).toBeVisible()
    await row.click({ position: { x: 8, y: 8 } })
    await expect(page.getByTestId('inbox-preview')).toContainText(title)
    await expect(page).toHaveURL(new RegExp(`task=${taskId}`))
    await expect(page.getByTestId('inbox-preview').getByRole('button', { name: 'Approve' })).toBeVisible()
  })
})
