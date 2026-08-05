// NewTaskModal tests: workflow is now chosen per-task rather than pinned to
// the board. Verify the workflow <select> renders sorted alphabetically,
// defaults to "Default" when present, shows all repos (not filtered by
// workflow), and submits the chosen workflow_id.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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

// issue #350 — NewTaskModal used to be a hand-rolled overlay with no dialog
// role, no Escape handling, and no focus containment. It's now built on the
// shared ModalShell.
describe('NewTaskModal dialog semantics (#350)', () => {
  beforeEach(() => {
    reposListMock.mockReset().mockResolvedValue([repo({ id: 'repo-1', name: 'repo-one' })])
    workflowsListMock.mockReset().mockResolvedValue([workflow({ id: 'wf-default', name: 'Default' })])
    templatesListMock.mockReset().mockResolvedValue([])
    useTasksStore.setState({ tasks: [], loading: false, error: null })
  })

  it('renders with dialog role and an accessible label', async () => {
    render(<NewTaskModal onClose={() => {}} />)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAttribute('aria-label', 'New Task')
  })

  it('uses "Duplicate Task" as the aria-label when duplicating', async () => {
    const source = task({ id: 'src-1' })
    render(<NewTaskModal source={source} onClose={() => {}} />)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveAttribute('aria-label', 'Duplicate Task')
  })

  it('calls onClose when Escape is pressed', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(<NewTaskModal onClose={onClose} />)
    await screen.findByRole('dialog')
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when the backdrop is clicked', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(<NewTaskModal onClose={onClose} />)
    const dialog = await screen.findByRole('dialog')
    await user.click(dialog)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('focuses the title input on open', async () => {
    render(<NewTaskModal onClose={() => {}} />)
    const titleInput = await screen.findByPlaceholderText('Short task description')
    expect(titleInput).toHaveFocus()
  })
})

// issue #350 — the unmount-cleanup effect used to list `attachmentPreviews`
// in its deps, so its cleanup ran before every array change (not just
// unmount), revoking still-mounted preview URLs. Attaching a second image
// used to revoke the first's URL while its <img> was still rendered.
describe('NewTaskModal attachment preview revocation (#350)', () => {
  let createSpy: ReturnType<typeof vi.spyOn>
  let revokeSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    reposListMock.mockReset().mockResolvedValue([repo({ id: 'repo-1', name: 'repo-one' })])
    workflowsListMock.mockReset().mockResolvedValue([workflow({ id: 'wf-default', name: 'Default' })])
    templatesListMock.mockReset().mockResolvedValue([])
    useTasksStore.setState({ tasks: [], loading: false, error: null })

    let n = 0
    createSpy = vi.spyOn(URL, 'createObjectURL').mockImplementation(() => `blob:preview-${++n}`)
    revokeSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  })

  afterEach(() => {
    // Use afterEach (not an in-test mockRestore()) so a failed assertion
    // mid-test can't leak these spies into the next test.
    createSpy.mockRestore()
    revokeSpy.mockRestore()
  })

  function makeImageFile(name: string): File {
    return new File(['fake-image-bytes'], name, { type: 'image/png' })
  }

  it('attaching a second image does not revoke the first preview URL', async () => {
    const user = userEvent.setup()
    render(<NewTaskModal onClose={() => {}} />)
    await screen.findByTestId('new-task-repo-select')

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement
    expect(fileInput).toBeTruthy()

    await user.upload(fileInput, makeImageFile('a.png'))
    expect(await screen.findByAltText('a.png')).toBeInTheDocument()
    expect(revokeSpy).not.toHaveBeenCalled()

    await user.upload(fileInput, makeImageFile('b.png'))
    await screen.findByAltText('b.png')

    // Attaching the second file must not have revoked the first's URL —
    // both thumbnails should still be backed by live object URLs.
    expect(revokeSpy).not.toHaveBeenCalled()
    expect(screen.getByAltText('a.png')).toHaveAttribute('src', 'blob:preview-1')
    expect(screen.getByAltText('b.png')).toHaveAttribute('src', 'blob:preview-2')
  })

  it('removeAttachment revokes exactly the removed URL, not survivors', async () => {
    const user = userEvent.setup()
    render(<NewTaskModal onClose={() => {}} />)
    await screen.findByTestId('new-task-repo-select')

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement
    await user.upload(fileInput, [makeImageFile('a.png'), makeImageFile('b.png')])
    await screen.findByAltText('a.png')
    await screen.findByAltText('b.png')

    // Remove the first attachment (a.png) — its "Remove" button is the first
    // of the two.
    await user.click(screen.getAllByTitle('Remove')[0])

    expect(revokeSpy).toHaveBeenCalledTimes(1)
    expect(revokeSpy).toHaveBeenCalledWith('blob:preview-1')
    // The survivor's preview is untouched.
    expect(screen.getByAltText('b.png')).toHaveAttribute('src', 'blob:preview-2')
  })

  it('revokes remaining preview URLs on unmount', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<NewTaskModal onClose={() => {}} />)
    await screen.findByTestId('new-task-repo-select')

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement
    await user.upload(fileInput, makeImageFile('a.png'))
    await screen.findByAltText('a.png')

    expect(revokeSpy).not.toHaveBeenCalled()
    unmount()
    expect(revokeSpy).toHaveBeenCalledWith('blob:preview-1')
  })

  // Defensive check (see `toSafePreviewSrc` in NewTaskModal.tsx): the
  // attachment thumbnail's `src` must only ever be a `blob:` URL created by
  // this component itself. If `URL.createObjectURL` is ever stubbed/replaced
  // with something that doesn't return a `blob:` string (accidentally or via
  // a future refactor), the guard must blank the `src` rather than pass an
  // untrusted string straight into the DOM.
  it('never renders a preview src that is not a blob: URL', async () => {
    createSpy.mockRestore()
    createSpy = vi.spyOn(URL, 'createObjectURL').mockImplementation(() => 'https://evil.example/x')

    const user = userEvent.setup()
    render(<NewTaskModal onClose={() => {}} />)
    await screen.findByTestId('new-task-repo-select')

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement
    await user.upload(fileInput, makeImageFile('a.png'))

    const img = await screen.findByAltText('a.png')
    // React omits the `src` attribute entirely for an empty string rather
    // than rendering `src=""` — either way, the untrusted URL never reaches
    // the DOM.
    expect(img.getAttribute('src')).not.toBe('https://evil.example/x')
    expect(img).not.toHaveAttribute('src', 'https://evil.example/x')
  })
})
