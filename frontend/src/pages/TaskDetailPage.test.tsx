// TaskDetailPage tab-switching tests: default tab is Overview; clicking
// Logs/Diff mounts RunLogPane/DiffReviewPane (identified via the
// data-testid hooks added to those components for testability) and hides
// the other tabs' content.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route, useNavigate } from 'react-router-dom'
import TaskDetailPage from './TaskDetailPage'
import type { Task, Workflow, AgentRun } from '../api/client'

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      tasks: {
        get: vi.fn(),
        runs: vi.fn(),
        listLabelHistory: vi.fn().mockResolvedValue([]),
        sourceComments: vi.fn().mockResolvedValue([]),
        subtasks: vi.fn().mockResolvedValue([]),
        dependencies: vi.fn().mockResolvedValue({ blocked_by: [], blocking: [], blocked_by_count: 0, blocking_count: 0 }),
        reviewComments: vi.fn().mockResolvedValue([]),
        diff: vi.fn().mockResolvedValue({ branch: 'main', diff: '' }),
        runLogs: vi.fn().mockResolvedValue({ items: [], hasMore: false, prevCursor: null }),
      },
      workflows: {
        get: vi.fn(),
      },
      repos: {
        list: vi.fn().mockResolvedValue([]),
      },
      agents: {
        list: vi.fn().mockResolvedValue([]),
      },
      github: {
        authStatus: vi.fn().mockResolvedValue({ authed: true, note: '' }),
      },
    },
  }
})

// TaskDetailPage renders several child components (SubtasksPanel,
// DependenciesPanel) that also register their own wsClient.on() handlers, so
// capturing only "the last registered handler" (as a single module-level
// variable) is unreliable — whichever component mounts/re-renders last wins,
// not necessarily TaskDetailPage's own handler. Instead, track every
// registered handler and broadcast simulated events to all of them, exactly
// as the real wsClient would. Each handler is expected to ignore events it
// doesn't recognize, so this mirrors production behavior.
const wsHandlers = new Set<(event: { type: string; payload: Record<string, unknown> }) => void>()
const wsUnsubscribe = vi.fn()
const wsSubscribeTask = vi.fn()
const wsUnsubscribeTask = vi.fn()

function fireWsEvent(event: { type: string; payload: Record<string, unknown> }) {
  for (const h of wsHandlers) h(event)
}

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn((h: (event: { type: string; payload: Record<string, unknown> }) => void) => {
      wsHandlers.add(h)
      return () => {
        wsHandlers.delete(h)
        wsUnsubscribe()
      }
    }),
    subscribeTask: (...args: unknown[]) => wsSubscribeTask(...args),
    unsubscribeTask: (...args: unknown[]) => wsUnsubscribeTask(...args),
  },
}))

import { api } from '../api/client'

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'A detailed task',
    description: 'Some description',
    type: 'feature',
    label: 'todo',
    repo_id: 'r1',
    workflow_id: 'wf',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

function workflow(): Workflow {
  return {
    id: 'wf',
    name: 'Default',
    description: '',
    labels: [
      { id: 'l1', workflow_id: 'wf', name: 'todo', color: '#000', sort_order: 0, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit: null, wip_limit_hard: 0 },
    ],
    transitions: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
}

function run(overrides: Partial<AgentRun> = {}): AgentRun {
  return {
    id: 'run-1',
    task_id: 'task-1',
    agent_config_id: 'a1',
    status: 'completed',
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

function renderPage(taskFixture: Task, runs: AgentRun[] = []) {
  vi.mocked(api.tasks.get).mockResolvedValue(taskFixture)
  vi.mocked(api.tasks.runs).mockResolvedValue({ items: runs, nextCursor: null })
  vi.mocked(api.workflows.get).mockResolvedValue(workflow())

  return render(
    <MemoryRouter initialEntries={[`/tasks/${taskFixture.id}`]}>
      <Routes>
        <Route path="/tasks/:id" element={<TaskDetailPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('TaskDetailPage tab switching', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    wsHandlers.clear()
    wsUnsubscribe.mockClear()
    wsSubscribeTask.mockClear()
    wsUnsubscribeTask.mockClear()
    vi.mocked(api.tasks.listLabelHistory).mockResolvedValue([])
    vi.mocked(api.tasks.sourceComments).mockResolvedValue([])
    vi.mocked(api.tasks.subtasks).mockResolvedValue([])
    vi.mocked(api.tasks.dependencies).mockResolvedValue({ blocked_by: [], blocking: [], blocked_by_count: 0, blocking_count: 0 })
    vi.mocked(api.tasks.reviewComments).mockResolvedValue([])
    vi.mocked(api.tasks.diff).mockResolvedValue({ branch: 'main', diff: '' })
    vi.mocked(api.tasks.runLogs).mockResolvedValue({ items: [], hasMore: false, prevCursor: null })
    vi.mocked(api.repos.list).mockResolvedValue([])
    vi.mocked(api.agents.list).mockResolvedValue([])
    vi.mocked(api.github.authStatus).mockResolvedValue({ authed: true, note: '' })
  })

  it('defaults to the Overview tab, showing the task title and no log/diff pane', async () => {
    renderPage(task())

    expect(await screen.findByText('A detailed task')).toBeInTheDocument()
    expect(screen.queryByTestId('run-log-pane')).not.toBeInTheDocument()
    expect(screen.queryByTestId('diff-review-pane')).not.toBeInTheDocument()
  })

  it('clicking the Logs tab mounts RunLogPane', async () => {
    const user = userEvent.setup()
    renderPage(task(), [run()])

    await screen.findByText('A detailed task')
    await user.click(screen.getByRole('button', { name: 'Logs' }))

    expect(await screen.findByTestId('run-log-pane')).toBeInTheDocument()
    expect(screen.queryByTestId('diff-review-pane')).not.toBeInTheDocument()
  })

  it('clicking the Diff tab mounts DiffReviewPane', async () => {
    const user = userEvent.setup()
    renderPage(task())

    await screen.findByText('A detailed task')
    await user.click(screen.getByRole('button', { name: 'Diff' }))

    expect(await screen.findByTestId('diff-review-pane')).toBeInTheDocument()
    expect(screen.queryByTestId('run-log-pane')).not.toBeInTheDocument()

    await waitFor(() => {
      expect(api.tasks.diff).toHaveBeenCalledWith('task-1')
    })
  })
})

describe('TaskDetailPage WS-driven state updates', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    wsHandlers.clear()
    wsUnsubscribe.mockClear()
    wsSubscribeTask.mockClear()
    wsUnsubscribeTask.mockClear()
    vi.mocked(api.tasks.listLabelHistory).mockResolvedValue([])
    vi.mocked(api.tasks.sourceComments).mockResolvedValue([])
    vi.mocked(api.tasks.subtasks).mockResolvedValue([])
    vi.mocked(api.tasks.dependencies).mockResolvedValue({ blocked_by: [], blocking: [], blocked_by_count: 0, blocking_count: 0 })
    vi.mocked(api.tasks.reviewComments).mockResolvedValue([])
    vi.mocked(api.tasks.diff).mockResolvedValue({ branch: 'main', diff: '' })
    vi.mocked(api.tasks.runLogs).mockResolvedValue({ items: [], hasMore: false, prevCursor: null })
    vi.mocked(api.repos.list).mockResolvedValue([])
    vi.mocked(api.agents.list).mockResolvedValue([])
    vi.mocked(api.github.authStatus).mockResolvedValue({ authed: true, note: '' })
  })

  it('subscribes to the task WS channel and registers a handler, cleaned up on unmount', async () => {
    const { unmount } = renderPage(task())
    await screen.findByText('A detailed task')

    expect(wsSubscribeTask).toHaveBeenCalledWith('task-1')
    expect(wsHandlers.size).toBeGreaterThan(0)

    unmount()
    expect(wsHandlers.size).toBe(0)
    expect(wsUnsubscribeTask).toHaveBeenCalledWith('task-1')
  })

  // Regression test for #249: a dead running-indicator caused by wsClient.on
  // being mocked as a permanent no-op in tests, so no WS-driven state update
  // was ever exercised. task.agent_started must refresh runs so a newly
  // running run becomes the active/selected run, and task.agent_done must
  // flip that run's status back off so the "Stop run" indicator disappears.
  it('task.agent_started refreshes runs so the running indicator appears, and task.agent_done clears it', async () => {
    renderPage(task(), [])
    await screen.findByText('A detailed task')

    // No runs yet — no "Stop run" indicator.
    expect(screen.queryByRole('button', { name: /Stop run/i })).not.toBeInTheDocument()

    // Simulate the run starting: refetch returns a running run.
    vi.mocked(api.tasks.runs).mockResolvedValue({ items: [run({ id: 'run-live', status: 'running' })], nextCursor: null })
    fireWsEvent({ type: 'task.agent_started', payload: { task_id: 'task-1', run_id: 'run-live' } })

    expect(await screen.findByRole('button', { name: /Stop run/i })).toBeInTheDocument()

    // Simulate completion: task.agent_done flips the tracked run's status.
    fireWsEvent({ type: 'task.agent_done', payload: { task_id: 'task-1', run_id: 'run-live', status: 'completed' } })

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /Stop run/i })).not.toBeInTheDocument()
    })
  })

  it('ignores WS events for a different task id', async () => {
    renderPage(task(), [run({ id: 'run-live', status: 'running' })])
    await screen.findByText('A detailed task')
    expect(await screen.findByRole('button', { name: /Stop run/i })).toBeInTheDocument()

    vi.mocked(api.tasks.get).mockClear()
    vi.mocked(api.tasks.runs).mockClear()
    fireWsEvent({ type: 'task.agent_started', payload: { task_id: 'some-other-task', run_id: 'run-x' } })

    // Give any accidental async handlers a chance to run, then assert no refetch happened.
    await Promise.resolve()
    await Promise.resolve()
    expect(api.tasks.get).not.toHaveBeenCalled()
    expect(api.tasks.runs).not.toHaveBeenCalled()
  })

  it('task.label_changed refetches the task and label history', async () => {
    renderPage(task())
    await screen.findByText('A detailed task')
    vi.mocked(api.tasks.get).mockClear()
    vi.mocked(api.tasks.listLabelHistory).mockClear()

    fireWsEvent({ type: 'task.label_changed', payload: { task_id: 'task-1', from: 'todo', to: 'doing' } })

    await waitFor(() => {
      expect(api.tasks.get).toHaveBeenCalledWith('task-1')
      expect(api.tasks.listLabelHistory).toHaveBeenCalledWith('task-1')
    })
  })

  it('task.git_state_changed updates git_state/pr_url without a refetch', async () => {
    renderPage(task())
    await screen.findByText('A detailed task')
    vi.mocked(api.tasks.get).mockClear()

    fireWsEvent({ type: 'task.git_state_changed', payload: { task_id: 'task-1', git_state: 'dirty', pr_url: 'https://example.com/pr/1' } })

    // This event patches local state directly rather than refetching.
    await Promise.resolve()
    expect(api.tasks.get).not.toHaveBeenCalled()
  })
})

// Regression tests for #341: React Router reuses this component instance
// across `/tasks/:id` navigations, so an in-flight response for the
// previously-viewed task must never be applied once the user has navigated
// to a different task.
describe('TaskDetailPage navigation races', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    wsHandlers.clear()
    wsUnsubscribe.mockClear()
    wsSubscribeTask.mockClear()
    wsUnsubscribeTask.mockClear()
    vi.mocked(api.tasks.listLabelHistory).mockResolvedValue([])
    vi.mocked(api.tasks.sourceComments).mockResolvedValue([])
    vi.mocked(api.tasks.subtasks).mockResolvedValue([])
    vi.mocked(api.tasks.dependencies).mockResolvedValue({ blocked_by: [], blocking: [], blocked_by_count: 0, blocking_count: 0 })
    vi.mocked(api.tasks.reviewComments).mockResolvedValue([])
    vi.mocked(api.tasks.diff).mockResolvedValue({ branch: 'main', diff: '' })
    vi.mocked(api.tasks.runLogs).mockResolvedValue({ items: [], hasMore: false, prevCursor: null })
    vi.mocked(api.repos.list).mockResolvedValue([])
    vi.mocked(api.agents.list).mockResolvedValue([])
    vi.mocked(api.github.authStatus).mockResolvedValue({ authed: true, note: '' })
  })

  it('never shows task A data (or its runs) after navigating to task B while A is still loading', async () => {
    const taskA = task({ id: 'task-A', title: 'Task A title' })
    const taskB = task({ id: 'task-B', title: 'Task B title' })
    const runA = run({ id: 'run-A', task_id: 'task-A' })
    const runB = run({ id: 'run-B', task_id: 'task-B' })

    // Task A's fetches never resolve until we manually flush them below —
    // this simulates a slow response landing after navigation.
    let resolveTaskA!: (t: Task) => void
    let resolveRunsA!: (r: { items: AgentRun[]; nextCursor: string | null }) => void
    const taskAPromise = new Promise<Task>((res) => { resolveTaskA = res })
    const runsAPromise = new Promise<{ items: AgentRun[]; nextCursor: string | null }>((res) => { resolveRunsA = res })

    vi.mocked(api.tasks.get).mockImplementation((id: string) =>
      id === 'task-A' ? taskAPromise : Promise.resolve(taskB))
    vi.mocked(api.tasks.runs).mockImplementation((id: string) =>
      id === 'task-A' ? runsAPromise : Promise.resolve({ items: [runB], nextCursor: null }))
    vi.mocked(api.workflows.get).mockResolvedValue(workflow())

    // A tiny harness that exposes a button to navigate within the *same*
    // MemoryRouter/history, so TaskDetailPage keeps the same component
    // instance across the navigation (mirroring React Router's real
    // behaviour, which a remounted MemoryRouter would not).
    function Harness() {
      const navigate = useNavigate()
      return (
        <>
          <button onClick={() => navigate('/tasks/task-B')}>go-to-b</button>
          <Routes>
            <Route path="/tasks/:id" element={<TaskDetailPage />} />
          </Routes>
        </>
      )
    }

    render(
      <MemoryRouter initialEntries={['/tasks/task-A']}>
        <Harness />
      </MemoryRouter>,
    )

    // Navigate to task B before A's fetch resolves.
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'go-to-b' }))

    expect(await screen.findByText('Task B title')).toBeInTheDocument()

    // Now let task A's stale responses land, and give React a chance to
    // flush any resulting state update.
    resolveTaskA(taskA)
    resolveRunsA({ items: [runA], nextCursor: null })
    await waitFor(() => {
      expect(screen.getByText('Task B title')).toBeInTheDocument()
    })

    // Task A's title must never appear, and task B's data must still be shown.
    expect(screen.queryByText('Task A title')).not.toBeInTheDocument()
  })

  // The initial-load effect already had a `cancelled` closure guard before
  // this fix, so the case above is covered even without currentIdRef. The
  // WS-driven refresh callbacks (refreshTask/refreshRuns/etc.) had no such
  // guard at all: a task.agent_started/task.label_changed/etc. event that
  // arrives for the *previously viewed* task while its refetch is in flight
  // must not overwrite task B's header/run list once the user has navigated
  // away from A. This is the case currentIdRef actually fixes.
  it('does not apply a stale WS-triggered refresh for task A after navigating to task B', async () => {
    const taskA = task({ id: 'task-A', title: 'Task A title' })
    const taskB = task({ id: 'task-B', title: 'Task B title' })
    const runB = run({ id: 'run-B', task_id: 'task-B' })

    // A resolves immediately on first load...
    vi.mocked(api.tasks.get).mockImplementation((id: string) =>
      Promise.resolve(id === 'task-A' ? taskA : taskB))
    vi.mocked(api.tasks.runs).mockImplementation((id: string) =>
      Promise.resolve({ items: id === 'task-A' ? [] : [runB], nextCursor: null }))
    vi.mocked(api.workflows.get).mockResolvedValue(workflow())

    function Harness() {
      const navigate = useNavigate()
      return (
        <>
          <button onClick={() => navigate('/tasks/task-B')}>go-to-b</button>
          <Routes>
            <Route path="/tasks/:id" element={<TaskDetailPage />} />
          </Routes>
        </>
      )
    }

    render(
      <MemoryRouter initialEntries={['/tasks/task-A']}>
        <Harness />
      </MemoryRouter>,
    )

    expect(await screen.findByText('Task A title')).toBeInTheDocument()

    // Now make task A's *next* fetch (triggered by a WS refresh) hang, so it
    // resolves only after we've navigated away.
    let resolveStaleTaskA!: (t: Task) => void
    const staleTaskAPromise = new Promise<Task>((res) => { resolveStaleTaskA = res })
    vi.mocked(api.tasks.get).mockImplementation((id: string) =>
      id === 'task-A' ? staleTaskAPromise : Promise.resolve(taskB))

    // Trigger refreshTask() for task A via a WS event (still viewing A).
    fireWsEvent({ type: 'task.updated', payload: { id: 'task-A' } })

    // Navigate away before that refetch resolves.
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'go-to-b' }))
    expect(await screen.findByText('Task B title')).toBeInTheDocument()

    // Now let A's stale refresh response land, and give React a chance to
    // flush any resulting state update (waitFor polls with act() wrapping).
    resolveStaleTaskA({ ...taskA, title: 'Task A title (updated)' })
    await waitFor(() => {
      // The mock has resolved; if the bug were present this would eventually
      // show "Task A title (updated)" instead.
      expect(screen.getByText('Task B title')).toBeInTheDocument()
    })
    expect(screen.queryByText('Task A title (updated)')).not.toBeInTheDocument()
  })

  it('resets run state on navigation so task B never shows task A stale run list while loading', async () => {
    const taskA = task({ id: 'task-A', title: 'Task A title' })
    const taskB = task({ id: 'task-B', title: 'Task B title' })
    const runA = run({ id: 'run-A', task_id: 'task-A', status: 'running' })

    vi.mocked(api.tasks.get).mockImplementation((id: string) =>
      Promise.resolve(id === 'task-A' ? taskA : taskB))
    vi.mocked(api.tasks.runs).mockImplementation((id: string) =>
      Promise.resolve({ items: id === 'task-A' ? [runA] : [], nextCursor: null }))
    vi.mocked(api.workflows.get).mockResolvedValue(workflow())

    function Harness() {
      const navigate = useNavigate()
      return (
        <>
          <button onClick={() => navigate('/tasks/task-B')}>go-to-b</button>
          <Routes>
            <Route path="/tasks/:id" element={<TaskDetailPage />} />
          </Routes>
        </>
      )
    }

    render(
      <MemoryRouter initialEntries={['/tasks/task-A']}>
        <Harness />
      </MemoryRouter>,
    )

    // Task A is running, so its "Stop run" indicator is visible.
    expect(await screen.findByRole('button', { name: /Stop run/i })).toBeInTheDocument()

    // Make task B's task fetch hang so we can inspect the state right after
    // navigation, before B's own data has loaded (runs resolves immediately
    // with an empty list — Promise.all still waits on the task fetch).
    let resolveTaskB!: (t: Task) => void
    const taskBPromise = new Promise<Task>((res) => { resolveTaskB = res })
    vi.mocked(api.tasks.get).mockImplementation((id: string) =>
      id === 'task-B' ? taskBPromise : Promise.resolve(taskA))
    vi.mocked(api.tasks.runs).mockImplementation((id: string) =>
      id === 'task-B' ? Promise.resolve({ items: [], nextCursor: null }) : Promise.resolve({ items: [runA], nextCursor: null }))

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'go-to-b' }))

    // While task B's fetch is in flight, the reset effect must already have
    // cleared task A's run state — no stale "Stop run" indicator, and the
    // page falls back to its loading state rather than showing A's data.
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /Stop run/i })).not.toBeInTheDocument()
    })
    expect(screen.queryByText('Task A title')).not.toBeInTheDocument()

    resolveTaskB(taskB)
    expect(await screen.findByText('Task B title')).toBeInTheDocument()
  })
})
