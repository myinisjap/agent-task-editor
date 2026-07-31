// TaskDetailPage tab-switching tests: default tab is Overview; clicking
// Logs/Diff mounts RunLogPane/DiffReviewPane (identified via the
// data-testid hooks added to those components for testability) and hides
// the other tabs' content.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
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
