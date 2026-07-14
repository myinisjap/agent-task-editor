import { render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { test, expect, vi } from 'vitest'
import ChatPage from './ChatPage'

// The backend marshals an empty list as JSON null (Go nil slice), so
// api.chat.list()/api.repos.list() can resolve to null. ChatPage must coerce
// these to [] — otherwise .find()/.map() throw and blank the page. This test
// pins that: it feeds null and asserts the page renders instead of crashing.
vi.mock('../api/client', () => ({
  api: {
    chat: { list: () => Promise.resolve(null), get: () => Promise.resolve({ session: null, messages: null }) },
    repos: { list: () => Promise.resolve(null) },
  },
}))
vi.mock('../api/ws', () => ({ wsClient: { on: () => () => {} } }))

test('renders without crashing when the API returns null lists', async () => {
  const { container } = render(<MemoryRouter><ChatPage /></MemoryRouter>)
  await waitFor(() => expect(container.textContent).toContain('New chat'))
  // Empty-state copy proves it rendered past the .find()/.map() calls.
  expect(container.textContent).toContain('Select a chat')
  // Mobile single-pane logic: with no chat open, the sidebar is shown (not
  // hidden) so the list gets the screen. (Class check, not computed layout —
  // jsdom doesn't evaluate media queries.)
  const sidebar = container.querySelector('.md\\:w-64')
  expect(sidebar?.className).toContain('flex')
  expect(sidebar?.className).not.toContain('hidden')
})
