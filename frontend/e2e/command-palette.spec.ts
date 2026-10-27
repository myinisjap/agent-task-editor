import { test, expect } from '@playwright/test'

// Smoke: open the command palette and navigate with it.
test.describe('command palette', () => {
  test('Ctrl/Cmd+K opens the palette and navigates', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name === 'mobile-chrome', 'keyboard flow is desktop only')
    await page.goto('board')
    await page.keyboard.press('ControlOrMeta+K')
    const dialog = page.getByRole('dialog', { name: 'Search and commands' })
    await expect(dialog).toBeVisible()
    await page.keyboard.type('Settings: Providers')
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/settings\/providers/)
  })

  test('phone: reachable from More as "Search and commands"', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile-chrome', 'phone only')
    await page.goto('board')
    await page.getByRole('button', { name: /more/i }).click()
    await page.getByRole('button', { name: /Search and commands/ }).click()
    await expect(page.getByRole('dialog', { name: 'Search and commands' })).toBeVisible()
  })
})
