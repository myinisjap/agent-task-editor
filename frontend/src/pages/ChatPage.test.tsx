import { render, waitFor, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { test, expect, vi, beforeEach } from 'vitest'
import ChatPage from './ChatPage'

// The backend marshals an empty list as JSON null (Go nil slice), so
// api.chat.list()/api.repos.list() can resolve to null. ChatPage must coerce
// these to [] — otherwise .find()/.map() throw and blank the page. The first
// test pins that: it feeds null and asserts the page renders instead of crashing.
let sessions: unknown = null
vi.mock('../api/client', () => ({
  api: {
    chat: { list: () => Promise.resolve(sessions), get: () => Promise.resolve({ session: null }) },
    repos: { list: () => Promise.resolve(null) },
    providerConfigs: { list: () => Promise.resolve(null) },
  },
}))
vi.mock('../api/ws', () => ({ wsTicketParam: () => Promise.resolve('') }))

beforeEach(() => { sessions = null })

test('renders without crashing when the API returns null lists', async () => {
  const { container } = render(<MemoryRouter><ChatPage /></MemoryRouter>)
  await waitFor(() => expect(container.textContent).toContain('New terminal'))
  // Empty-state copy proves it rendered past the .find()/.map() calls.
  expect(container.textContent).toContain('Select a terminal')
  // Mobile single-pane logic: with no chat open, the sidebar is shown (not
  // hidden) so the list gets the screen. (Class check, not computed layout —
  // jsdom doesn't evaluate media queries — but the element is located by a
  // stable testid rather than a Tailwind layout class.)
  const sidebar = screen.getByTestId('chat-sidebar')
  expect(sidebar.className).toContain('flex')
  expect(sidebar.className).not.toContain('hidden')
})

test('opening a session mounts the on-screen key bar with the terminal', async () => {
  // Phone keyboards have no Esc/Tab/arrows, so the bar ships with every
  // terminal — this pins that it is actually wired into TerminalView.
  sessions = [{ id: 's1', repo_id: 'r1', provider_config_id: 'p1', title: 'demo' }]
  render(<MemoryRouter><ChatPage /></MemoryRouter>)
  fireEvent.click(await screen.findByText('demo'))
  const bar = await screen.findByTestId('terminal-key-bar')
  expect(bar.className).toContain('md:hidden') // mobile only
  expect(screen.getByRole('button', { name: 'Shift+Tab' })).toBeInTheDocument()
})
