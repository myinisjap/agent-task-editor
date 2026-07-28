// BoardPage bulk-action tests: select two cards, run a bulk action, assert
// api.tasks.bulk is called with the right (ids, action, opts) shape, and
// that a partial-failure response surfaces the error banner without
// clearing the selection.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import BoardPage from './BoardPage'
import type { Task, Workflow } from '../api/client'
import { useTasksStore } from '../stores/tasks'
import { useWorkflowStore } from '../stores/workflow'
import { useReposStore } from '../stores/repos'

const bulkMock = vi.fn()
const costByTaskMock = vi.fn()
const tasksListMock = vi.fn()
const workflowsListMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      tasks: {
        list: (...args: unknown[]) => tasksListMock(...args),
        get: vi.fn(),
        bulk: (...args: unknown[]) => bulkMock(...args),
        setPaused: vi.fn(),
        setArchived: vi.fn(),
        update: vi.fn(),
        delete: vi.fn(),
      },
      workflows: { list: (...args: unknown[]) => workflowsListMock(...args) },
      repos: { list: vi.fn().mockResolvedValue([]) },
      dashboard: { costByTask: (...args: unknown[]) => costByTaskMock(...args) },
      // OnboardingChecklist (rendered by BoardPage) calls these on mount.
      providerConfigs: { list: vi.fn().mockResolvedValue([]) },
      agents: { list: vi.fn().mockResolvedValue([]) },
      health: { providers: vi.fn().mockResolvedValue({ checks: [] }) },
    },
  }
})

// wsOnHandler captures the handler BoardPage registers via wsClient.on so
// tests can simulate incoming WS events without a real socket.
let wsOnHandler: ((event: { type: string; payload: unknown }) => void) | null = null
// wsStatusHandler captures the handler BoardPage registers via
// wsClient.onStatusChange so tests can simulate connect/disconnect.
let wsStatusHandler: ((status: 'connecting' | 'open' | 'closed') => void) | null = null

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn((h: (event: { type: string; payload: unknown }) => void) => {
      wsOnHandler = h
      return () => {}
    }),
    onStatusChange: vi.fn((h: (status: 'connecting' | 'open' | 'closed') => void) => {
      wsStatusHandler = h
      return () => {}
    }),
    getStatus: vi.fn(() => 'open'),
    subscribeTask: vi.fn(),
    unsubscribeTask: vi.fn(),
  },
}))

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: overrides.id ?? 'task-1',
    title: overrides.title ?? 'Task one',
    description: '',
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
      { id: 'l2', workflow_id: 'wf', name: 'doing', color: '#000', sort_order: 1, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit: null, wip_limit_hard: 0 },
    ],
    transitions: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
}

function seedStores(tasks: Task[]) {
  // BoardPage's mount effects call fetchTasks()/fetchWorkflows()/fetchRepos(),
  // which overwrite any pre-seeded zustand state with whatever the mocked
  // api.* calls resolve — so the fixtures need to flow through those mocks,
  // not just through .setState() before render.
  tasksListMock.mockResolvedValue({ items: tasks, nextCursor: null })
  workflowsListMock.mockResolvedValue([workflow()])
  useTasksStore.setState({ tasks: [], loading: false, error: null })
  useWorkflowStore.setState({
    workflows: [],
    loading: false,
    selectedId: 'wf',
  })
  useReposStore.setState({ repos: [], loading: false, error: null })
}

async function selectTwoCards(user: ReturnType<typeof userEvent.setup>) {
  // BoardPage kicks off tasks.fetch() on mount, which sets loading: true
  // until the mocked api.tasks.list() resolves — wait for the cards to
  // actually mount before trying to select them.
  const checkboxes = await screen.findAllByTitle('Select for bulk actions')
  await user.click(checkboxes[0])
  await user.click(checkboxes[1])
}

describe('BoardPage bulk actions', () => {
  beforeEach(() => {
    bulkMock.mockReset()
    costByTaskMock.mockReset().mockResolvedValue([])
    tasksListMock.mockReset()
    workflowsListMock.mockReset()
    seedStores([task({ id: 'task-1', title: 'Task one' }), task({ id: 'task-2', title: 'Task two' })])
  })

  it('selecting two cards and clicking Pause calls api.tasks.bulk with the selected ids', async () => {
    bulkMock.mockResolvedValue({ results: [{ id: 'task-1', ok: true }, { id: 'task-2', ok: true }] })
    const user = userEvent.setup()

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await selectTwoCards(user)
    expect(screen.getByText('2 selected')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Pause/ }))

    await waitFor(() => {
      expect(bulkMock).toHaveBeenCalledWith(expect.arrayContaining(['task-1', 'task-2']), 'pause', undefined)
    })
    const [ids] = bulkMock.mock.calls[0]
    expect(ids).toHaveLength(2)
  })

  it('selecting two cards and clicking Archive calls api.tasks.bulk with action "archive"', async () => {
    bulkMock.mockResolvedValue({ results: [{ id: 'task-1', ok: true }, { id: 'task-2', ok: true }] })
    const user = userEvent.setup()

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await selectTwoCards(user)
    // Two buttons match /Archive/: the filter-bar "🗄 Archived" toggle and
    // the bulk-toolbar "🗄 Archive" action — match the exact bulk-action
    // label to disambiguate.
    await user.click(screen.getByRole('button', { name: '🗄 Archive' }))

    await waitFor(() => {
      expect(bulkMock).toHaveBeenCalledWith(expect.arrayContaining(['task-1', 'task-2']), 'archive', undefined)
    })
  })

  it('using the "Move to…" select calls api.tasks.bulk with action "move" and the chosen label', async () => {
    bulkMock.mockResolvedValue({ results: [{ id: 'task-1', ok: true }, { id: 'task-2', ok: true }] })
    const user = userEvent.setup()

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await selectTwoCards(user)
    const moveSelect = screen.getByDisplayValue('Move to…')
    await user.selectOptions(moveSelect, 'doing')

    await waitFor(() => {
      expect(bulkMock).toHaveBeenCalledWith(expect.arrayContaining(['task-1', 'task-2']), 'move', { to_label: 'doing' })
    })
  })

  it('shows the failure banner and keeps the selection when a bulk result has a failure', async () => {
    bulkMock.mockResolvedValue({
      results: [
        { id: 'task-1', ok: true },
        { id: 'task-2', ok: false, error: 'boom' },
      ],
    })
    const user = userEvent.setup()

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await selectTwoCards(user)
    await user.click(screen.getByRole('button', { name: /Pause/ }))

    await waitFor(() => {
      expect(screen.getByText(/1 of 2 failed: boom/)).toBeInTheDocument()
    })
    // Selection is NOT cleared on partial failure.
    expect(screen.getByText('2 selected')).toBeInTheDocument()
  })
})

describe('BoardPage task.created_bulk handling', () => {
  beforeEach(() => {
    bulkMock.mockReset()
    costByTaskMock.mockReset().mockResolvedValue([])
    tasksListMock.mockReset()
    workflowsListMock.mockReset()
    wsOnHandler = null
    seedStores([task({ id: 'task-1', title: 'Task one' })])
  })

  it('a single task.created_bulk event triggers exactly one task list refresh, not one fetch per id', async () => {
    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(wsOnHandler).not.toBeNull()
    })
    // Mount's own fetchTasks() call.
    const callsAfterMount = tasksListMock.mock.calls.length
    expect(callsAfterMount).toBeGreaterThan(0)

    tasksListMock.mockResolvedValue({
      items: [task({ id: 'task-1' }), task({ id: 'task-2' }), task({ id: 'task-3' })],
      nextCursor: null,
    })

    wsOnHandler!({
      type: 'task.created_bulk',
      payload: { repo_id: 'r1', source: 'github', count: 2, ids: ['task-2', 'task-3'] },
    })

    await waitFor(() => {
      expect(tasksListMock.mock.calls.length).toBe(callsAfterMount + 1)
    })
    await waitFor(() => {
      expect(useTasksStore.getState().tasks.map((t) => t.id)).toEqual(
        expect.arrayContaining(['task-1', 'task-2', 'task-3']),
      )
    })
  })
})

// Regression guard for #249 — runningTaskIds was dead state and the "Agent
// running" pulse dot never rendered. These tests drive the board through
// real task.agent_started / task.agent_done WS events (captured via the
// wsClient.on mock) and through the active_agent_run_id seed path.
describe('BoardPage running indicator (#249)', () => {
  beforeEach(() => {
    bulkMock.mockReset()
    costByTaskMock.mockReset().mockResolvedValue([])
    tasksListMock.mockReset()
    workflowsListMock.mockReset()
    wsOnHandler = null
    wsStatusHandler = null
  })

  it('shows the running dot after task.agent_started and clears it after task.agent_done', async () => {
    seedStores([task({ id: 'task-1', title: 'Task one' })])

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await waitFor(() => expect(wsOnHandler).not.toBeNull())
    expect(screen.queryByTitle('Agent running')).not.toBeInTheDocument()

    wsOnHandler!({
      type: 'task.agent_started',
      payload: { task_id: 'task-1', run_id: 'r1', agent_name: 'a' },
    })

    expect(await screen.findByTitle('Agent running')).toBeInTheDocument()

    wsOnHandler!({
      type: 'task.agent_done',
      payload: { task_id: 'task-1', run_id: 'r1', status: 'success' },
    })

    await waitFor(() => {
      expect(screen.queryByTitle('Agent running')).not.toBeInTheDocument()
    })
  })

  it('seeds the running dot from active_agent_run_id on load, without any WS event', async () => {
    seedStores([task({ id: 'task-1', title: 'Task one', active_agent_run_id: 'run-9' })])

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    expect(await screen.findByTitle('Agent running')).toBeInTheDocument()
  })

  it('clears the running dot when the WS connection drops', async () => {
    seedStores([task({ id: 'task-1', title: 'Task one' })])

    render(
      <MemoryRouter>
        <BoardPage />
      </MemoryRouter>,
    )

    await waitFor(() => expect(wsOnHandler).not.toBeNull())
    await waitFor(() => expect(wsStatusHandler).not.toBeNull())

    wsOnHandler!({
      type: 'task.agent_started',
      payload: { task_id: 'task-1', run_id: 'r1', agent_name: 'a' },
    })
    expect(await screen.findByTitle('Agent running')).toBeInTheDocument()

    wsStatusHandler!('closed')

    await waitFor(() => {
      expect(screen.queryByTitle('Agent running')).not.toBeInTheDocument()
    })
  })
})
