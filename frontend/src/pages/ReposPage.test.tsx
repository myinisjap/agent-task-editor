// ReposPage tests focus on the "Runtime image" field: setting it on create,
// clearing/updating it on an existing repo via PATCH, and making sure an
// unrelated edit doesn't clobber the stored value.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ReposPage from './ReposPage'
import type { Repo, Workflow } from '../api/client'

const listMock = vi.fn()
const createMock = vi.fn()
const updateMock = vi.fn()
const deleteMock = vi.fn()
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
})

describe('ReposPage runtime image field', () => {
  it('sets runtime_image on create', async () => {
    createMock.mockResolvedValue(repo({ id: 'repo-2', name: 'acme/other', runtime_image: 'ghcr.io/example/runtime:1' }))

    render(<ReposPage />)
    await screen.findByText('acme/widgets')

    await userEvent.click(screen.getByText('+ Add Repo'))
    await userEvent.type(screen.getByPlaceholderText('org/repo'), 'acme/other')
    await userEvent.type(screen.getByPlaceholderText('Leave blank to auto-clone via Remote URL'), '/tmp/other')
    await userEvent.type(screen.getByPlaceholderText('ghcr.io/example/runtime:latest'), 'ghcr.io/example/runtime:1')

    await userEvent.click(screen.getByText('Add Repo'))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const body = createMock.mock.calls[0][0]
    expect(body.runtime_image).toBe('ghcr.io/example/runtime:1')
  })

  it('sets and clears runtime_image on an existing repo via PATCH', async () => {
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

    // Clearing the field sends an empty string, not an omitted key — matches
    // runtime_image's *string preserve-if-omitted vs. explicit-clear contract.
    updateMock.mockClear()
    await userEvent.click(screen.getByText('Edit'))
    const imageInput2 = screen.getByPlaceholderText('ghcr.io/example/runtime:latest')
    await userEvent.clear(imageInput2)
    await userEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    expect(updateMock.mock.calls[0][1].runtime_image).toBe('')
  })

  it('preserves runtime_image when saving an unrelated field edit', async () => {
    const seeded = repo({ runtime_image: 'ghcr.io/example/runtime:1' })
    listMock.mockResolvedValue([seeded])
    updateMock.mockResolvedValue(seeded)

    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('Edit'))

    // Touch an unrelated field (name) and save without touching runtime image.
    const nameInputs = screen.getAllByDisplayValue('acme/widgets')
    const nameInput = nameInputs[nameInputs.length - 1]
    await userEvent.type(nameInput, '-renamed')
    await userEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(updateMock).toHaveBeenCalled())
    expect(updateMock.mock.calls[0][1].runtime_image).toBe('ghcr.io/example/runtime:1')
  })
})
