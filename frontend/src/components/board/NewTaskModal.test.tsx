// NewTaskModal tests: workflow is now chosen per-task rather than pinned to
// the board. Verify the workflow <select> renders sorted alphabetically,
// defaults to "Default" when present, shows all repos (not filtered by
// workflow), and submits the chosen workflow_id.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import NewTaskModal from './NewTaskModal'
import type { Repo, Task, Workflow } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'

const reposListMock = vi.fn()
const workflowsListMock = vi.fn()
const templatesListMock = vi.fn()
const createMock = vi.fn()

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      repos: { list: (...args: unknown[]) => reposListMock(...args) },
      workflows: { list: (...args: unknown[]) => workflowsListMock(...args) },
      templates: { list: (...args: unknown[]) => templatesListMock(...args) },
      tasks: { create: (...args: unknown[]) => createMock(...args) },
    },
  }
})

function repo(overrides: Partial<Repo> = {}): Repo {
  return {
    id: overrides.id ?? 'repo-1',
    name: overrides.name ?? 'repo-one',
    path: '/tmp/repo-one',
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: overrides.id ?? 'source-task-1',
    title: overrides.title ?? 'Fix the flaky test',
    description: overrides.description ?? 'It fails about 1 in 10 runs on CI',
    type: overrides.type ?? 'bug',
    priority: overrides.priority ?? 1,
    label: overrides.label ?? 'in_progress',
    repo_id: overrides.repo_id ?? 'repo-1',
    workflow_id: overrides.workflow_id ?? 'wf-default',
    created_at: overrides.created_at ?? new Date().toISOString(),
    updated_at: overrides.updated_at ?? new Date().toISOString(),
    ...overrides,
  }
}

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: overrides.id ?? 'wf-default',
    name: overrides.name ?? 'Default',
    description: '',
    labels: [],
    transitions: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

describe('NewTaskModal', () => {
  beforeEach(() => {
    reposListMock.mockReset().mockResolvedValue([repo({ id: 'repo-1', name: 'repo-one' }), repo({ id: 'repo-2', name: 'repo-two' })])
    workflowsListMock.mockReset().mockResolvedValue([
      workflow({ id: 'wf-zebra', name: 'Zebra' }),
      workflow({ id: 'wf-default', name: 'Default' }),
      workflow({ id: 'wf-alpha', name: 'Alpha' }),
    ])
    templatesListMock.mockReset().mockResolvedValue([])
    createMock.mockReset().mockResolvedValue({
      id: 'task-1',
      title: 'New task',
      description: '',
      type: 'feature',
      label: 'not_ready',
      repo_id: 'repo-1',
      workflow_id: 'wf-default',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    })
    useTasksStore.setState({ tasks: [], loading: false, error: null })
  })

  it('shows all repos regardless of their workflow (repos are no longer workflow-scoped)', async () => {
    render(<NewTaskModal onClose={() => {}} />)

    const repoSelect = await screen.findByTestId('new-task-repo-select')
    await waitFor(() => {
      expect(repoSelect.querySelectorAll('option')).toHaveLength(2)
    })
  })

  it('renders the workflow select sorted alphabetically and defaults to "Default"', async () => {
    render(<NewTaskModal onClose={() => {}} />)

    const workflowSelect = await screen.findByTestId('new-task-workflow-select') as HTMLSelectElement
    await waitFor(() => {
      const options = Array.from(workflowSelect.querySelectorAll('option')).map((o) => o.textContent)
      expect(options).toEqual(['Alpha', 'Default', 'Zebra'])
    })
    expect(workflowSelect.value).toBe('wf-default')
  })

  it('falls back to the alphabetically-first workflow when none is named "Default"', async () => {
    workflowsListMock.mockResolvedValue([
      workflow({ id: 'wf-zebra', name: 'Zebra' }),
      workflow({ id: 'wf-alpha', name: 'Alpha' }),
    ])
    render(<NewTaskModal onClose={() => {}} />)

    const workflowSelect = await screen.findByTestId('new-task-workflow-select') as HTMLSelectElement
    await waitFor(() => {
      expect(workflowSelect.value).toBe('wf-alpha')
    })
  })

  it('submits the chosen workflow_id, not the board-hinted one, when the user changes the select', async () => {
    const user = userEvent.setup()
    // Board hints at "Zebra" (e.g. the currently active board workflow).
    render(<NewTaskModal workflow={workflow({ id: 'wf-zebra', name: 'Zebra' })} onClose={() => {}} />)

    const workflowSelect = await screen.findByTestId('new-task-workflow-select') as HTMLSelectElement
    await waitFor(() => expect(workflowSelect.value).toBe('wf-zebra'))

    await user.selectOptions(workflowSelect, 'wf-alpha')

    const titleInput = screen.getByPlaceholderText('Short task description')
    await user.type(titleInput, 'Do the thing')

    await user.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const [body] = createMock.mock.calls[0]
    expect(body).toBeInstanceOf(FormData)
    expect((body as FormData).get('workflow_id')).toBe('wf-alpha')
  })

  it('uses the board-hinted workflow as the initial selection when present in the list', async () => {
    render(<NewTaskModal workflow={workflow({ id: 'wf-zebra', name: 'Zebra' })} onClose={() => {}} />)

    const workflowSelect = await screen.findByTestId('new-task-workflow-select') as HTMLSelectElement
    await waitFor(() => expect(workflowSelect.value).toBe('wf-zebra'))
  })

  describe('duplicate mode (source prop)', () => {
    it('pre-fills title (suffixed "(copy)"), description, type, priority, repo, and workflow from the source task, and shows "Duplicate Task" as the heading', async () => {
      const source = task({
        title: 'Fix the flaky test',
        description: 'It fails about 1 in 10 runs on CI',
        type: 'bug',
        priority: 2,
        repo_id: 'repo-2',
        workflow_id: 'wf-alpha',
      })

      render(<NewTaskModal source={source} onClose={() => {}} />)

      expect(screen.getByText('Duplicate Task')).toBeInTheDocument()

      const titleInput = screen.getByPlaceholderText('Short task description') as HTMLInputElement
      expect(titleInput.value).toBe('Fix the flaky test (copy)')

      const descInput = screen.getByPlaceholderText('Additional context for the agent (optional)') as HTMLTextAreaElement
      expect(descInput.value).toBe('It fails about 1 in 10 runs on CI')

      const repoSelect = await screen.findByTestId('new-task-repo-select') as HTMLSelectElement
      await waitFor(() => expect(repoSelect.value).toBe('repo-2'))

      const workflowSelect = await screen.findByTestId('new-task-workflow-select') as HTMLSelectElement
      await waitFor(() => expect(workflowSelect.value).toBe('wf-alpha'))
    })

    it('falls back to the first repo when the source repo no longer exists', async () => {
      const source = task({ repo_id: 'repo-does-not-exist' })
      render(<NewTaskModal source={source} onClose={() => {}} />)

      const repoSelect = await screen.findByTestId('new-task-repo-select') as HTMLSelectElement
      await waitFor(() => expect(repoSelect.value).toBe('repo-1'))
    })

    it('falls back to the default workflow-resolution logic when the source workflow no longer exists', async () => {
      const source = task({ workflow_id: 'wf-does-not-exist' })
      render(<NewTaskModal source={source} onClose={() => {}} />)

      const workflowSelect = await screen.findByTestId('new-task-workflow-select') as HTMLSelectElement
      await waitFor(() => expect(workflowSelect.value).toBe('wf-default'))
    })

    it('starts with no attachments even though the source task may have had some', async () => {
      const source = task({ attachments: ['uploads/foo.png'] })
      render(<NewTaskModal source={source} onClose={() => {}} />)

      // No attachment thumbnails are rendered.
      expect(screen.queryByAltText('foo.png')).not.toBeInTheDocument()
      expect(screen.queryAllByRole('img')).toHaveLength(0)
    })

    it('submits the copied fields via api.tasks.create without mutating the source task', async () => {
      const user = userEvent.setup()
      const source = task({
        title: 'Fix the flaky test',
        description: 'It fails about 1 in 10 runs on CI',
        type: 'bug',
        priority: 2,
        repo_id: 'repo-1',
        workflow_id: 'wf-default',
      })
      const sourceSnapshot = JSON.parse(JSON.stringify(source))

      render(<NewTaskModal source={source} onClose={() => {}} />)

      const titleInput = await screen.findByDisplayValue('Fix the flaky test (copy)')
      await user.click(screen.getByRole('button', { name: 'Create' }))

      await waitFor(() => expect(createMock).toHaveBeenCalled())
      const [body] = createMock.mock.calls[0]
      expect(body).toBeInstanceOf(FormData)
      const fd = body as FormData
      expect(fd.get('title')).toBe('Fix the flaky test (copy)')
      expect(fd.get('description')).toBe('It fails about 1 in 10 runs on CI')
      expect(fd.get('type')).toBe('bug')
      expect(fd.get('priority')).toBe('2')
      expect(fd.get('repo_id')).toBe('repo-1')
      expect(fd.get('workflow_id')).toBe('wf-default')
      expect(fd.getAll('attachments')).toHaveLength(0)

      // The source task object passed in is untouched.
      expect(source).toEqual(sourceSnapshot)
      expect(titleInput).toBeInTheDocument()
    })
  })
})
