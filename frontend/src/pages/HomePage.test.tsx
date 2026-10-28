import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, renderHook, fireEvent, within, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import HomePage from './HomePage'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore, useInboxCount } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { useWorkflowStore } from '../stores/workflow'

let dashValue: Record<string, unknown>
vi.mock('../lib/useDashboard', () => ({
  useDashboard: () => ({ dash: dashValue, refresh: () => {} }),
}))
vi.mock('../components/TaskFactory', () => ({ default: () => <div data-testid="scene" /> }))
vi.mock('../components/FactoryLine', () => ({ default: () => <div data-testid="scene" /> }))
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      needsMe: { list: () => Promise.resolve([]) },
      tasks: { list: () => Promise.resolve({ items: [], nextCursor: null }), reviewBrief: () => Promise.resolve({ human_gate: false }), previews: () => Promise.resolve({ capture: null }) },
      health: { providers: () => Promise.resolve({ checks: [], paused_providers: [] }) },
      workflows: { list: () => Promise.resolve([]) },
    },
  }
})

const NAMES = ['agent-review', 'done', 'not_ready', 'plan', 'review', 'review-plan', 'testing', 'work']
const workflow = {
  id: 'w', name: 'w', description: '', created_at: '', updated_at: '',
  labels: NAMES.map((name, i) => ({ id: name, workflow_id: 'w', name, color: '#336699', sort_order: i, agent_ignore: 0, is_terminal: name === 'done' ? 1 : 0 })),
  transitions: [{ from_label: 'review', to_label: 'done', trigger_type: 'human', path: 'success' }],
}

const agent = (n: number) => ({ run_id: `r${n}`, task_id: `t${n}`, task_title: `Task ${n}`, agent_name: 'coder', started_at: new Date(Date.now() - 65_000).toISOString() })

beforeEach(() => {
  localStorage.clear()
  dashValue = { label_counts: { review: 2 }, workflow_label_counts: {}, active_agents: [], repo_concurrency: [], cost_by_day: [] }
  const old = new Date(Date.now() - 2 * 3600_000).toISOString()
  const item = { task_id: 'a', task_title: 'Approve A', repo_id: 'r', workflow_id: 'w', label: 'l', reason: 'human_gate', since: old }
  useNeedsMeStore.setState({ items: [item], byTask: {}, loaded: true, refresh: async () => {} } as never)
  useInboxStore.setState({
    failedEffects: [{ task: { id: 'f', title: 'F' }, effects: [], since: new Date().toISOString() }],
    sourceEdits: [],
    loaded: true,
    refresh: async () => {},
  } as never)
  useHealthStore.setState({
    paused: [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }],
    loaded: true,
    fetch: async () => {},
  } as never)
  useWorkflowStore.setState({ workflows: [workflow], selectedId: 'w' } as never)
})

afterEach(() => vi.unstubAllGlobals())

const renderPage = () => render(<MemoryRouter><HomePage /></MemoryRouter>)

describe('HomePage', () => {
  it('opens the review popup from a row and closes it with Escape', async () => {
    render(<MemoryRouter><HomePage /></MemoryRouter>)
    fireEvent.click(screen.getByRole('button', { name: /Approve A/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Needs you review' })
    expect(within(dialog).getByRole('heading', { name: /Needs you · 1 of 3/ })).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('shows the shared count, summary, button rows and no inline actions', () => {
    const shared = renderHook(() => useInboxCount()).result.current
    renderPage()
    expect(screen.getByTestId('needs-you-count')).toHaveTextContent(String(shared))
    expect(screen.getByTestId('needs-you-summary')).toHaveTextContent('Decide 1 · Fix setup 2')
    expect(screen.getByRole('button', { name: /Approve A/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^(approve|reject)$/i })).toBeNull()
  })

  it('renders unknown reasons with a tag and marks old entries as stale', () => {
    useNeedsMeStore.setState({
      items: [{ task_id: 'z', task_title: 'Auto', repo_id: 'r', workflow_id: 'w', label: 'l', reason: 'auto_approving', since: new Date(Date.now() - 3 * 3600_000).toISOString() }],
    } as never)
    renderPage()
    const row = screen.getByRole('button', { name: /Auto/ })
    expect(within(row).getByText(/Answer|Fix setup|Decide/)).toBeInTheDocument()
    expect(row.querySelector('[data-stale="true"]')).not.toBeNull()
  })

  it('pads with free slots when a worker limit is known', () => {
    dashValue.active_agents = [agent(1)]
    dashValue.repo_concurrency = [{ repo_id: 'r', repo_name: 'repo', in_use: 1, limit: 3 }]
    renderPage()
    expect(screen.getAllByTestId('running-row')).toHaveLength(1)
    expect(screen.getAllByTestId('free-slot')).toHaveLength(2)
    expect(screen.getByTestId('workers')).toHaveTextContent('Workers 1 / 3')
    expect(screen.getByText(/^1:0[56]$/)).toBeInTheDocument()
  })

  it('shows only running rows when no limit is known', () => {
    dashValue.active_agents = [agent(1)]
    renderPage()
    expect(screen.queryAllByTestId('free-slot')).toHaveLength(0)
    expect(screen.getByTestId('workers')).toHaveTextContent('Workers 1')
    expect(screen.getByTestId('workers')).not.toHaveTextContent('/')
  })

  it('shows the dispatch pill paused with the reason', () => {
    renderPage()
    expect(screen.getByTestId('dispatch-pill')).toHaveTextContent('Dispatch paused · x')
  })

  it('shows running when nothing is paused', () => {
    useHealthStore.setState({ paused: [] } as never)
    renderPage()
    expect(screen.getByTestId('dispatch-pill')).toHaveTextContent('Dispatch running')
  })

  it('falls back to cost_by_day for Today', () => {
    dashValue.cost_by_day = [{ day: new Date().toISOString().slice(0, 10), cost_usd: 1.5, run_count: 3, input_tokens: 0, output_tokens: 0 }]
    renderPage()
    expect(screen.getByTestId('today-spend')).toHaveTextContent('$1.50 today · 3 runs')
  })
})

describe('scene collapse', () => {
  const stubCompact = (compact: boolean) =>
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: compact && query.includes('max-height'),
      media: query, addEventListener: () => {}, removeEventListener: () => {},
    }))

  it('is expanded by default on a wide screen', () => {
    renderPage()
    expect(screen.getByTestId('scene')).toBeInTheDocument()
    expect(screen.getByTestId('station-legend')).toBeInTheDocument()
  })

  it('starts collapsed on a compact viewport and persists the toggle', () => {
    stubCompact(true)
    renderPage()
    expect(screen.getByTestId('pipeline-strip')).toBeInTheDocument()
    expect(screen.queryByTestId('scene')).toBeNull()
    fireEvent.click(screen.getByTestId('scene-toggle'))
    expect(screen.getByTestId('scene')).toBeInTheDocument()
    expect(localStorage.getItem('home.scene.expanded.compact')).toBe('1')
  })
})

describe('HomePage live layer', () => {
  it('shows done today and verify pass rate', () => {
    dashValue = { ...dashValue, today: { done_count: 3, verify_total: 4, verify_passed: 3, runs_started: 5 } }
    renderPage()
    expect(screen.getByTestId('today-done').textContent).toBe('3')
    expect(screen.getByText('done today')).toBeTruthy()
    expect(screen.getByTestId('today-verify').textContent).toBe('75%')
  })

  it('shows an en dash with no verifications or no today block', () => {
    dashValue = { ...dashValue, today: { done_count: 0, verify_total: 0, verify_passed: 0, runs_started: 0 } }
    const { unmount } = renderPage()
    expect(screen.getByTestId('today-verify').textContent).toBe('–')
    unmount()
    dashValue = { ...dashValue, today: undefined }
    renderPage()
    expect(screen.getByTestId('today-verify').textContent).toBe('–')
  })

  it('renders the empty ticker and tolerates a rejecting wake lock', () => {
    vi.stubGlobal('navigator', { ...navigator, wakeLock: { request: () => Promise.reject(new Error('no')) } })
    renderPage()
    expect(screen.getByTestId('recent-ticker').textContent).toContain('No activity yet')
  })
})

describe('RecentTicker', () => {
  const items = [3, 2, 1].map((n) => ({ id: String(n), text: `event ${n}`, at: n * 1000 }))
  it('renders up to 3 items, or only the latest', async () => {
    const { default: RecentTicker } = await import('../components/home/RecentTicker')
    const { unmount } = render(<RecentTicker items={items} />)
    expect(screen.getAllByTestId('ticker-item')).toHaveLength(3)
    unmount()
    render(<RecentTicker items={items} latestOnly />)
    expect(screen.getAllByTestId('ticker-item')).toHaveLength(1)
  })
})
