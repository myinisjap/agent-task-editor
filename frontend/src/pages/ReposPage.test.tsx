// ReposPage tests cover the runtime environment picker: three mutually
// exclusive modes (none / image ref / languages) on both the create and edit
// forms, backed by runtime_image + runtime_languages.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ReposPage from './ReposPage'
import type { Repo, Workflow } from '../api/client'

const listMock = vi.fn()
const createMock = vi.fn()
const updateMock = vi.fn()
const deleteMock = vi.fn()
const devcontainerMock = vi.fn()
const detectLanguagesMock = vi.fn()
const workflowsListMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      repos: {
        ...actual.api.repos,
        list: (...args: unknown[]) => listMock(...args),
        create: (...args: unknown[]) => createMock(...args),
        update: (...args: unknown[]) => updateMock(...args),
        delete: (...args: unknown[]) => deleteMock(...args),
        devcontainer: (...args: unknown[]) => devcontainerMock(...args),
        detectLanguages: (...args: unknown[]) => detectLanguagesMock(...args),
      },
      workflows: { ...actual.api.workflows, list: (...args: unknown[]) => workflowsListMock(...args) },
    },
  }
})

function repo(overrides: Partial<Repo> = {}): Repo {
  return {
    id: 'repo-1',
    name: 'acme/widgets',
    path: '/tmp/widgets',
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf-1',
    name: 'Default',
    description: '',
    labels: [],
    transitions: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

beforeEach(() => {
  listMock.mockReset().mockResolvedValue([repo()])
  workflowsListMock.mockReset().mockResolvedValue([workflow()])
  createMock.mockReset()
  updateMock.mockReset()
  deleteMock.mockReset()
  devcontainerMock.mockReset().mockResolvedValue({ source: 'none', effective_json: '', repo_file_present: false })
  detectLanguagesMock.mockReset()
})

describe('ReposPage runtime environment picker — create form', () => {
  async function openCreateForm() {
    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('+ Add Repo'))
    await userEvent.type(screen.getByPlaceholderText('org/repo'), 'acme/other')
    await userEvent.type(screen.getByPlaceholderText('Leave blank to auto-clone via Remote URL'), '/tmp/other')
  }

  it('defaults to None and sends empty runtime_image/runtime_languages', async () => {
    createMock.mockResolvedValue(repo({ id: 'repo-2', name: 'acme/other' }))
    await openCreateForm()

    await userEvent.click(screen.getByText('Add Repo'))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const body = createMock.mock.calls[0][0]
    expect(body.runtime_image).toBe('')
    expect(body.runtime_languages).toEqual([])
  })

  it('switching to Image ref mode sends the typed image and empty languages', async () => {
    createMock.mockResolvedValue(repo({ id: 'repo-2', name: 'acme/other', runtime_image: 'ghcr.io/example/runtime:1' }))
    await openCreateForm()

    await userEvent.click(screen.getByText('Image ref'))
    await userEvent.type(screen.getByPlaceholderText('ghcr.io/example/runtime:latest'), 'ghcr.io/example/runtime:1')
    await userEvent.click(screen.getByText('Add Repo'))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const body = createMock.mock.calls[0][0]
    expect(body.runtime_image).toBe('ghcr.io/example/runtime:1')
    expect(body.runtime_languages).toEqual([])
  })

  it('switching to Languages mode: add, edit, and remove a language row', async () => {
    createMock.mockResolvedValue(repo({ id: 'repo-2', name: 'acme/other' }))
    await openCreateForm()

    await userEvent.click(screen.getByText('Languages'))
    await userEvent.click(screen.getByText('+ add language'))

    // First row defaults to Go; set its version.
    const versionInputs = screen.getAllByPlaceholderText('version')
    await userEvent.type(versionInputs[0], '1.26')

    // Add a second row — should default to a different id (Node, since Go is used).
    await userEvent.click(screen.getByText('+ add language'))
    const selects = screen.getAllByRole('combobox').filter((el) => within(el).queryByText('Go'))
    expect(selects[0]).toHaveValue('go')
    const secondSelect = screen.getAllByRole('combobox').find((el) => (el as HTMLSelectElement).value === 'node')
    expect(secondSelect).toBeTruthy()

    // Remove the second row before submitting (the form closes on a
    // successful create, so there is no "submit twice" round trip here).
    // Narrow to the language row's "Remove <Language>" aria-label — the
    // repo list card below also has a plain "Remove" button.
    await userEvent.click(screen.getByRole('button', { name: 'Remove Node' }))

    await userEvent.click(screen.getByText('Add Repo'))
    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const body = createMock.mock.calls[0][0]
    expect(body.runtime_image).toBe('')
    expect(body.runtime_languages).toEqual([{ id: 'go', version: '1.26' }])
  })

  it('does not discard a typed image ref when switching modes and back', async () => {
    createMock.mockResolvedValue(repo({ id: 'repo-2', name: 'acme/other', runtime_image: 'ghcr.io/example/runtime:1' }))
    await openCreateForm()

    await userEvent.click(screen.getByText('Image ref'))
    await userEvent.type(screen.getByPlaceholderText('ghcr.io/example/runtime:latest'), 'ghcr.io/example/runtime:1')

    // Switch away and back — the typed value should still be there.
    await userEvent.click(screen.getByText('Languages'))
    await userEvent.click(screen.getByText('Image ref'))
    expect(screen.getByPlaceholderText('ghcr.io/example/runtime:latest')).toHaveValue('ghcr.io/example/runtime:1')

    await userEvent.click(screen.getByText('Add Repo'))
    await waitFor(() => expect(createMock).toHaveBeenCalled())
    expect(createMock.mock.calls[0][0].runtime_image).toBe('ghcr.io/example/runtime:1')
  })
})

describe('ReposPage runtime environment picker — edit form', () => {
  it('preselects Image ref mode from a stored runtime_image and PATCHes it on save', async () => {
    const seeded = repo({ runtime_image: 'ghcr.io/example/runtime:1' })
    listMock.mockResolvedValue([seeded])
    updateMock.mockResolvedValue({ ...seeded, runtime_image: 'ghcr.io/example/runtime:2' })

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))

    const imageInput = screen.getByPlaceholderText('ghcr.io/example/runtime:latest') as HTMLInputElement
    expect(imageInput.value).toBe('ghcr.io/example/runtime:1')

    await userEvent.clear(imageInput)
    await userEvent.type(imageInput, 'ghcr.io/example/runtime:2')
    await userEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    expect(updateMock.mock.calls[0][1].runtime_image).toBe('ghcr.io/example/runtime:2')
    expect(updateMock.mock.calls[0][1].runtime_languages).toEqual([])
  })

  it('preselects Languages mode from a stored runtime_languages list', async () => {
    const seeded = repo({ runtime_languages: [{ id: 'python', version: '3.12' }] })
    listMock.mockResolvedValue([seeded])
    updateMock.mockResolvedValue(seeded)

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))

    await waitFor(() => expect(devcontainerMock).toHaveBeenCalledWith('repo-1'))

    const versionInput = screen.getByPlaceholderText('version') as HTMLInputElement
    expect(versionInput.value).toBe('3.12')

    await userEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    expect(updateMock.mock.calls[0][1].runtime_languages).toEqual([{ id: 'python', version: '3.12' }])
    expect(updateMock.mock.calls[0][1].runtime_image).toBe('')
  })

  it('shows the repo_file_present warning when the devcontainer endpoint reports it', async () => {
    const seeded = repo({ runtime_languages: [{ id: 'go', version: '1.26' }] })
    listMock.mockResolvedValue([seeded])
    devcontainerMock.mockResolvedValue({ source: 'repo_file', effective_json: '{}', repo_file_present: true })

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))

    await screen.findByText(/this repo ships .devcontainer\/devcontainer.json/)
  })

  it('does not show the repo_file_present warning when absent', async () => {
    const seeded = repo({ runtime_languages: [{ id: 'go', version: '1.26' }] })
    listMock.mockResolvedValue([seeded])
    devcontainerMock.mockResolvedValue({ source: 'languages', effective_json: '{}', repo_file_present: false })

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))

    await waitFor(() => expect(devcontainerMock).toHaveBeenCalled())
    expect(screen.queryByText(/this repo ships .devcontainer\/devcontainer.json/)).toBeNull()
  })

  it('surfaces an API 400 inline instead of swallowing it', async () => {
    const seeded = repo({ runtime_languages: [{ id: 'go', version: '1.26' }] })
    listMock.mockResolvedValue([seeded])
    updateMock.mockRejectedValue(new Error('invalid runtime_languages entry: unknown id "cobol"'))

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))
    await userEvent.click(screen.getByText('Save'))

    await screen.findByText('Error: invalid runtime_languages entry: unknown id "cobol"')
  })

  it('switching an existing repo from Languages to None to Languages preserves the language rows', async () => {
    const seeded = repo({ runtime_languages: [{ id: 'ruby', version: '3.3' }] })
    listMock.mockResolvedValue([seeded])
    updateMock.mockResolvedValue(seeded)

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))

    await waitFor(() => expect(devcontainerMock).toHaveBeenCalled())

    await userEvent.click(screen.getByText('None — run in the backend container'))
    await userEvent.click(screen.getByText('Languages'))

    const versionInput = screen.getByPlaceholderText('version') as HTMLInputElement
    expect(versionInput.value).toBe('3.3')

    await userEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    expect(updateMock.mock.calls[0][1].runtime_languages).toEqual([{ id: 'ruby', version: '3.3' }])
  })

  it('switching to None sends empty runtime_image and runtime_languages', async () => {
    const seeded = repo({ runtime_image: 'ghcr.io/example/runtime:1' })
    listMock.mockResolvedValue([seeded])
    updateMock.mockResolvedValue({ ...seeded, runtime_image: '' })

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))
    await userEvent.click(screen.getByText('None — run in the backend container'))
    await userEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    expect(updateMock.mock.calls[0][1].runtime_image).toBe('')
    expect(updateMock.mock.calls[0][1].runtime_languages).toEqual([])
  })
})

describe('ReposPage — Detect from repo', () => {
  async function openLanguagesEdit(seeded: Repo) {
    listMock.mockResolvedValue([seeded])
    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))
    await waitFor(() => expect(devcontainerMock).toHaveBeenCalled())
    await userEvent.click(screen.getByText('Languages'))
  }

  it('clicking Detect populates rows from the response and shows source', async () => {
    detectLanguagesMock.mockResolvedValue({
      suggestions: [{ id: 'go', version: '1.26', source: 'go.mod', ambiguous: false }],
      used_llm: false,
    })
    await openLanguagesEdit(repo())

    await userEvent.click(screen.getByText('Detect from repo'))

    await waitFor(() => expect(detectLanguagesMock).toHaveBeenCalledWith('repo-1'))
    const versionInput = await screen.findByPlaceholderText('version') as HTMLInputElement
    expect(versionInput.value).toBe('1.26')
    await screen.findByText(/from go\.mod/)
  })

  it('flags ambiguous and empty-version rows visibly', async () => {
    detectLanguagesMock.mockResolvedValue({
      suggestions: [{ id: 'rust', version: '', source: 'Cargo.toml', ambiguous: true }],
      used_llm: false,
    })
    await openLanguagesEdit(repo())

    await userEvent.click(screen.getByText('Detect from repo'))

    await screen.findByText(/needs confirmation/)
    await screen.findByText(/no version detected, pick one/)
  })

  it('shows the used_llm notice when true and not when false', async () => {
    detectLanguagesMock.mockResolvedValue({
      suggestions: [{ id: 'go', version: '1.26', source: 'claude', ambiguous: false }],
      used_llm: true,
    })
    await openLanguagesEdit(repo())

    await userEvent.click(screen.getByText('Detect from repo'))

    await screen.findByText('suggested by Claude — please confirm')
  })

  it('does not show the used_llm notice when false', async () => {
    detectLanguagesMock.mockResolvedValue({
      suggestions: [{ id: 'go', version: '1.26', source: 'go.mod', ambiguous: false }],
      used_llm: false,
    })
    await openLanguagesEdit(repo())

    await userEvent.click(screen.getByText('Detect from repo'))

    await waitFor(() => expect(detectLanguagesMock).toHaveBeenCalled())
    expect(screen.queryByText('suggested by Claude — please confirm')).toBeNull()
  })

  it('a failed detect request shows an inline error and does not clear the form', async () => {
    detectLanguagesMock.mockRejectedValue(new Error('scan failed: permission denied'))
    const seeded = repo({ runtime_languages: [{ id: 'python', version: '3.12' }] })
    await openLanguagesEdit(seeded)

    const versionInput = screen.getByPlaceholderText('version') as HTMLInputElement
    expect(versionInput.value).toBe('3.12')

    await userEvent.click(screen.getByText('Detect from repo'))

    await screen.findByText('Error: scan failed: permission denied')
    // Existing row survives the failed detect.
    expect((screen.getByPlaceholderText('version') as HTMLInputElement).value).toBe('3.12')
  })

  it('nothing is persisted until Save — Detect alone does not PATCH', async () => {
    detectLanguagesMock.mockResolvedValue({
      suggestions: [{ id: 'go', version: '1.26', source: 'go.mod', ambiguous: false }],
      used_llm: false,
    })
    await openLanguagesEdit(repo())

    await userEvent.click(screen.getByText('Detect from repo'))

    await waitFor(() => expect(detectLanguagesMock).toHaveBeenCalled())
    expect(updateMock).not.toHaveBeenCalled()
  })

  it('detecting an id the user already has replaces that row instead of duplicating it', async () => {
    detectLanguagesMock.mockResolvedValue({
      suggestions: [{ id: 'python', version: '3.13', source: 'pyproject.toml', ambiguous: false }],
      used_llm: false,
    })
    const seeded = repo({ runtime_languages: [{ id: 'python', version: '3.10' }] })
    await openLanguagesEdit(seeded)

    await userEvent.click(screen.getByText('Detect from repo'))

    await waitFor(() => expect(detectLanguagesMock).toHaveBeenCalled())
    const versionInputs = screen.getAllByPlaceholderText('version') as HTMLInputElement[]
    expect(versionInputs).toHaveLength(1)
    expect(versionInputs[0].value).toBe('3.13')
  })
})
