import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ReposPage from './ReposPage'
import type { Repo, Workflow } from '../api/client'

const listMock = vi.fn()
const createMock = vi.fn()
const updateMock = vi.fn()
const deleteMock = vi.fn()
const detectRuntimeMock = vi.fn()
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
        detectRuntime: (...args: unknown[]) => detectRuntimeMock(...args),
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

describe('ReposPage — Agent runtime section', () => {
  beforeEach(() => {
    listMock.mockReset().mockResolvedValue([repo()])
    workflowsListMock.mockReset().mockResolvedValue([workflow()])
    createMock.mockReset()
    updateMock.mockReset()
    deleteMock.mockReset()
    detectRuntimeMock.mockReset()
  })

  it('renders the Agent runtime section on the create form with add/remove rows', async () => {
    render(<ReposPage />)
    await screen.findByText('acme/widgets')

    await userEvent.click(screen.getByText('+ Add Repo'))
    expect(screen.getByText('Agent runtime')).toBeInTheDocument()
    expect(screen.getByText(/Tasks and chat sessions on this repo run with these toolchains/)).toBeInTheDocument()

    await userEvent.click(screen.getByText('+ Add language'))
    const versionInput = screen.getByPlaceholderText('e.g. 1.21')
    expect(versionInput).toBeInTheDocument()

    const removeButton = within(versionInput.closest('.flex.items-center.gap-2') as HTMLElement).getByText('Remove')
    await userEvent.click(removeButton)
    expect(screen.queryByPlaceholderText('e.g. 1.21')).not.toBeInTheDocument()
  })

  it('prevents adding duplicate languages by disabling already-used options', async () => {
    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('+ Add Repo'))

    await userEvent.click(screen.getByText('+ Add language'))
    const select = screen.getByPlaceholderText('e.g. 1.21').closest('div')!.querySelector('select') as HTMLSelectElement
    expect(select.value).toBe('go')

    await userEvent.click(screen.getByText('+ Add language'))
    const selects = screen.getAllByPlaceholderText('e.g. 1.21').map((input) =>
      input.closest('div')!.querySelector('select') as HTMLSelectElement,
    )
    expect(selects[0].value).toBe('go')
    expect(selects[1].value).toBe('node')
    // The "go" option is disabled in the second row's select.
    const goOption = within(selects[1]).getByRole('option', { name: 'go' }) as HTMLOptionElement
    expect(goOption.disabled).toBe(true)
  })

  it('blocks save on an invalid version and shows an inline error', async () => {
    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('+ Add Repo'))

    await userEvent.type(screen.getByPlaceholderText('org/repo'), 'acme/new')
    await userEvent.type(screen.getByPlaceholderText(/Leave blank to auto-clone/), '/tmp/new')
    await userEvent.click(screen.getByText('+ Add language'))
    await userEvent.type(screen.getByPlaceholderText('e.g. 1.21'), '-bad')

    await userEvent.click(screen.getByText('Add Repo'))

    expect((await screen.findAllByText(/Invalid version for go/)).length).toBeGreaterThan(0)
    expect(createMock).not.toHaveBeenCalled()
  })

  it('includes runtime_languages pins in the create payload', async () => {
    createMock.mockResolvedValue(repo({ id: 'repo-2', name: 'acme/new' }))
    render(<ReposPage />)
    await screen.findByText('acme/widgets')
    await userEvent.click(screen.getByText('+ Add Repo'))

    await userEvent.type(screen.getByPlaceholderText('org/repo'), 'acme/new')
    await userEvent.type(screen.getByPlaceholderText(/Leave blank to auto-clone/), '/tmp/new')
    await userEvent.click(screen.getByText('+ Add language'))
    await userEvent.type(screen.getByPlaceholderText('e.g. 1.21'), '1.21')

    await userEvent.click(screen.getByText('Add Repo'))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const body = createMock.mock.calls[0][0]
    expect(body.runtime_languages).toEqual([{ id: 'go', version: '1.21' }])
  })

  it('detect fills rows from the mocked response and shows the source hint', async () => {
    detectRuntimeMock.mockResolvedValue({
      suggestions: [{ id: 'go', version: '1.21', source: 'go.mod' }],
    })
    render(<ReposPage />)
    await screen.findByText('acme/widgets')

    await userEvent.click(screen.getByText('Edit'))
    await userEvent.click(screen.getByText('Detect from repo'))

    await waitFor(() => expect(detectRuntimeMock).toHaveBeenCalledWith('repo-1'))
    expect(await screen.findByText('from go.mod')).toBeInTheDocument()
  })

  it('shows "Nothing detected." when detection returns no suggestions', async () => {
    detectRuntimeMock.mockResolvedValue({ suggestions: [] })
    render(<ReposPage />)
    await screen.findByText('acme/widgets')

    await userEvent.click(screen.getByText('Edit'))
    await userEvent.click(screen.getByText('Detect from repo'))

    expect(await screen.findByText('Nothing detected.')).toBeInTheDocument()
  })
})
