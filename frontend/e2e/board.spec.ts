import { test, expect } from '@playwright/test'

// A handful of high-value smoke flows exercised against the built
// docker-compose stack (nginx-served frontend + real Go backend + sqlite),
// not mocked in any way. Kept to one linear scenario — board load -> create
// task -> open task detail -> Logs tab mounts the WS log pane — per the
// scope note in the tracking issue: a few paths, fast, non-flaky. No real
// agent runs are triggered (no LLM credentials in CI).

test.describe('board and task-detail smoke flow', () => {
  test('load board, create a task, open its detail page, and mount the log pane', async ({ page }) => {
    const taskTitle = `E2E smoke task ${Date.now()}`

    // 1. Load board
    await page.goto('board')
    await expect(page.getByRole('heading', { name: 'Board', exact: true })).toBeVisible()
    await expect(page.getByText('not_ready', { exact: true })).toBeVisible()

    // 2. Create a task
    const addTaskButton = page.getByRole('button', { name: '+ Add task' })
    await addTaskButton.click()

    const titleInput = page.getByPlaceholder('Short task description')
    await expect(titleInput).toBeVisible()
    await titleInput.fill(taskTitle)

    // NewTaskModal auto-selects the first repo registered against the
    // active workflow, which may not be the repo global-setup.ts seeded if
    // others already exist (e.g. a stack left running from a prior local
    // run) — select it explicitly by name so the test doesn't depend on
    // registration order. Scoped to the modal (via the title input's form)
    // since the board itself also has a repo-filter <select> listing the
    // same repo names.
    const modalForm = titleInput.locator('xpath=ancestor::form')
    const repoOption = modalForm.locator('option', { hasText: 'e2e-demo-repo' })
    if (await repoOption.count()) {
      const repoSelect = modalForm.locator('select').filter({ has: repoOption })
      await repoSelect.selectOption({ label: 'e2e-demo-repo' })
    }

    const createButton = page.getByRole('button', { name: 'Create', exact: true })
    await expect(createButton).toBeEnabled()
    await createButton.click()

    // Modal closes and the new task appears on the board.
    await expect(page.getByPlaceholder('Short task description')).not.toBeVisible()
    const taskCard = page.getByText(taskTitle, { exact: true })
    await expect(taskCard).toBeVisible()

    // 3. Open task detail
    await taskCard.click()
    await expect(page).toHaveURL(/\/tasks\/[^/]+$/)
    await expect(page.getByRole('heading', { name: taskTitle, exact: true })).toBeVisible()

    // 4. Verify the WS log pane mounts on the Logs tab
    await page.getByRole('button', { name: 'Logs', exact: true }).click()
    await expect(page.getByTestId('run-log-pane')).toBeVisible()
  })
})
