import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import DashboardPage from './DashboardPage'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore, useInboxCount } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { renderHook } from '@testing-library/react'

vi.mock('../lib/useDashboard', () => ({
  useDashboard: () => ({ dash: { label_counts: {}, active_agents: [], repo_concurrency: [] }, refresh: () => {} }),
}))
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      needsMe: { list: () => Promise.resolve([]) },
      tasks: { list: () => Promise.resolve({ items: [], nextCursor: null }) },
      health: { providers: () => Promise.resolve({ checks: [], paused_providers: [] }) },
    },
  }
})

beforeEach(() => {
  const item = { task_id: 'a', task_title: 'A', repo_id: 'r', workflow_id: 'w', label: 'l', reason: 'human_gate', since: '2026-01-01T00:00:00Z' }
  // Refreshes are mocked out so the seeded state is what renders.
  useNeedsMeStore.setState({ items: [item], byTask: {}, refresh: async () => {} } as never)
  useInboxStore.setState({
    failedEffects: [{ task: { id: 'f' }, effects: [], since: '2026-01-01T00:00:00Z' }],
    sourceEdits: [],
    loaded: true,
    refresh: async () => {},
  } as never)
  useHealthStore.setState({
    paused: [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }],
    loaded: true,
    fetch: async () => {},
  } as never)
})

describe('DashboardPage needs-you card', () => {
  it('shows the shared count, links to the inbox and has no inline actions', () => {
    const shared = renderHook(() => useInboxCount()).result.current
    render(<MemoryRouter><DashboardPage /></MemoryRouter>)
    const card = screen.getByTestId('overview-needs-you')
    expect(shared).toBe(3)
    expect(card).toHaveTextContent(`Needs you (${shared})`)
    expect(card).toHaveTextContent('Decide 1 · Fix setup 2')
    expect(card).toHaveAttribute('href', '/inbox')
    expect(screen.queryByRole('button', { name: /approve|reject/i })).toBeNull()
  })
})
