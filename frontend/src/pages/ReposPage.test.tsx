// ReposPage tests focus on the "Runtime environment" picker: switching
// between None / Image ref / Dev container, the language picker writing
// into the raw devcontainer.json `features` block, the round-trip rule
// (picker edits must not clobber hand-written keys), invalid-JSON handling,
// and the repo-file-wins warning sourced from GET /repos/{id}/devcontainer.
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
  devcontainerMock.mockReset().mockResolvedValue({ source: 'db', effective_json: '', repo_file_present: false })
})

async function openEditForm() {
  render(<ReposPage />)
  await screen.findByText('acme/widgets')
  await userEvent.click(screen.getByText('Edit'))
  await waitFor(() => expect(devcontainerMock).toHaveBeenCalled())
}

describe('ReposPage runtime environment picker', () => {
  it('defaults to "None" and can switch to Image ref and Dev container', async () => {
    await openEditForm()

    const noneRadio = screen.getByLabelText(/None — run in the backend container/)
    expect(noneRadio).toBeChecked()

    const imageInput = screen.getByPlaceholderText('ghcr.io/me/img:1')
    await userEvent.type(imageInput, 'ghcr.io/me/img:1')
    // Typing into the image field selects that mode.
    expect(screen.getByLabelText(/Image ref:/)).toBeChecked()

    await userEvent.click(screen.getByLabelText(/Dev container/))
    expect(screen.getByLabelText(/Dev container/)).toBeChecked()
    expect(screen.getByText('Languages')).toBeInTheDocument()
  })

  it('adding a language via the picker writes the right features entry, and editing the version updates it', async () => {
    await openEditForm()
    await userEvent.click(screen.getByLabelText(/Dev container/))

    const addSelect = screen.getByDisplayValue('+ add language')
    await userEvent.selectOptions(addSelect, 'go')

    const versionInput = screen.getByPlaceholderText('version (e.g. 1.26)')
    await userEvent.type(versionInput, '1.26')

    await userEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    const body = updateMock.mock.calls[0][1]
    const parsed = JSON.parse(body.devcontainer_json)
    expect(parsed.features['ghcr.io/devcontainers/features/go:1']).toEqual({ version: '1.26' })
  })

  it('round-trips JSON to picker and back, preserving unmodelled keys untouched', async () => {
    const seeded = repo({
      devcontainer_json: JSON.stringify({
        features: {
          'ghcr.io/devcontainers/features/python:1': { version: '3.12' },
        },
        postCreateCommand: 'echo hello',
        mounts: ['source=foo,target=/foo,type=bind'],
      }),
    })
    listMock.mockResolvedValue([seeded])

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))
    await waitFor(() => expect(devcontainerMock).toHaveBeenCalled())

    // Dev container mode should already be selected since devcontainer_json is set.
    expect(screen.getByLabelText(/Dev container/)).toBeChecked()
    // Picker shows the seeded language.
    expect(screen.getByDisplayValue('3.12')).toBeInTheDocument()

    // Edit the version through the picker.
    const versionInput = screen.getByDisplayValue('3.12')
    await userEvent.clear(versionInput)
    await userEvent.type(versionInput, '3.13')

    await userEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    const body = updateMock.mock.calls[0][1]
    const parsed = JSON.parse(body.devcontainer_json)
    expect(parsed.features['ghcr.io/devcontainers/features/python:1']).toEqual({ version: '3.13' })
    // Unmodelled keys survive untouched.
    expect(parsed.postCreateCommand).toBe('echo hello')
    expect(parsed.mounts).toEqual(['source=foo,target=/foo,type=bind'])
  })

  it('surfaces invalid JSON typed into the raw editor as an error instead of crashing or saving', async () => {
    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))
    await waitFor(() => expect(devcontainerMock).toHaveBeenCalled())
    await userEvent.click(screen.getByLabelText(/Dev container/))
    await userEvent.click(screen.getByText(/Advanced: edit raw devcontainer.json/))

    const textarea = screen.getByLabelText('Raw devcontainer.json') as HTMLTextAreaElement
    await userEvent.clear(textarea)
    await userEvent.type(textarea, '{{ not valid json')

    // The editor renders inline near the textarea as soon as the JSON fails to parse.
    await waitFor(() => {
      const errorParagraph = textarea.parentElement?.querySelector('p.text-red-400')
      expect(errorParagraph).not.toBeNull()
      expect(errorParagraph?.textContent).not.toBe('')
    })

    await userEvent.click(screen.getByText('Save'))
    expect(updateMock).not.toHaveBeenCalled()
  })

  it('shows the repo-file-wins warning when the effective config endpoint reports repo_file_present', async () => {
    devcontainerMock.mockResolvedValue({ source: 'repo_file', effective_json: '{}', repo_file_present: true })
    await openEditForm()
    await userEvent.click(screen.getByLabelText(/Dev container/))

    expect(
      screen.getByText(/This repo ships \.devcontainer\/devcontainer\.json/),
    ).toBeInTheDocument()
  })

  it('does not lose typed devcontainer JSON when switching to None and back', async () => {
    await openEditForm()
    await userEvent.click(screen.getByLabelText(/Dev container/))
    await userEvent.click(screen.getByText(/Advanced: edit raw devcontainer.json/))

    const textarea = screen.getByLabelText('Raw devcontainer.json')
    await userEvent.clear(textarea)
    await userEvent.type(textarea, '{{"postCreateCommand":"echo hi"}}')

    await userEvent.click(screen.getByLabelText(/None — run in the backend container/))
    await userEvent.click(screen.getByLabelText(/Dev container/))

    const restoredTextarea = await screen.findByLabelText('Raw devcontainer.json')
    expect(within(restoredTextarea.closest('div') as HTMLElement).getByDisplayValue(/echo hi/)).toBeInTheDocument()
  })
})
