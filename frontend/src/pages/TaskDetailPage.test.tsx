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

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn(() => () => {}),
    subscribeTask: vi.fn(),
    unsubscribeTask: vi.fn(),
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
      { id: 'l1', workflow_id: 'wf', name: 'todo', color: '#000', sort_order: 0, agent_ignore: 0, is_terminal: 0, create_pr: 0 },
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
  vi.mocked(api.tasks.runs).mockResolvedValue(runs)
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
    vi.mocked(api.tasks.listLabelHistory).mockResolvedValue([])
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
