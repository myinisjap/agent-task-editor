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
    },
  }
})

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn(() => () => {}),
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
      { id: 'l1', workflow_id: 'wf', name: 'todo', color: '#000', sort_order: 0, agent_ignore: 0, is_terminal: 0, create_pr: 0 },
      { id: 'l2', workflow_id: 'wf', name: 'doing', color: '#000', sort_order: 1, agent_ignore: 0, is_terminal: 0, create_pr: 0 },
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
