import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

// Home's Needs-you panel opens a review popup (dialog on desktop, bottom sheet
// on phones) and keeps ?task=<id> in the URL. /inbox redirects to Home.
const API_BASE = process.env.E2E_API_BASE_URL ?? 'http://localhost:8080/api/v1'

test.describe('needs-you review popup', () => {
  let taskId = ''
  const title = `E2E review ${Date.now()}`

  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('board.onboarding.dismissed', 'true'))
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

  test('clicking a row opens the popup; Escape closes it', async ({ page }) => {
    await page.goto('')
    await page.getByTestId('needs-you-panel').getByRole('button', { name: new RegExp(title) }).click()
    const dialog = page.getByRole('dialog', { name: 'Needs you review' })
    await expect(dialog).toContainText(title)
    await expect(page).toHaveURL(new RegExp(`task=${taskId}`))
    await expect(dialog.getByRole('button', { name: 'Approve' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await expect(page).not.toHaveURL(/task=/)
  })

  test('/inbox?task=<id> redirects to Home with the popup open', async ({ page }) => {
    await page.goto(`inbox?task=${taskId}`)
    await expect(page).toHaveURL(new RegExp(`/\\?task=${taskId}`))
    await expect(page.getByRole('dialog', { name: 'Needs you review' })).toBeVisible()
  })

  test('the open popup has no contrast violations', async ({ page }) => {
    await page.goto(`?task=${taskId}`)
    const dialog = page.getByRole('dialog', { name: 'Needs you review' })
    await expect(dialog).toContainText(title)
    const res = await new AxeBuilder({ page }).include('[role="dialog"]').withRules(['color-contrast']).analyze()
    expect(res.violations).toEqual([])
  })
})
