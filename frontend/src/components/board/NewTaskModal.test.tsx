// NewTaskModal tests: workflow is now chosen per-task rather than pinned to
// the board. Verify the workflow <select> renders sorted alphabetically,
// defaults to "Default" when present, shows all repos (not filtered by
// workflow), and submits the chosen workflow_id.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import NewTaskModal from './NewTaskModal'
import type { Repo, Workflow } from '../../api/client'
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
})
