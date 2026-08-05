// issue #350 — the "New Workflow" modal used to be a hand-rolled overlay
// with only a Cancel button: no dialog role, no Escape, and no
// backdrop-click dismissal at all. It's now built on the shared ModalShell.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import WorkflowPage from './WorkflowPage'
import type { Workflow } from '../api/client'
import { useWorkflowStore } from '../stores/workflow'

const workflowsListMock = vi.fn()
const workflowsGetMock = vi.fn()
const workflowsCreateMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      workflows: {
        ...actual.api.workflows,
        list: (...args: unknown[]) => workflowsListMock(...args),
        get: (...args: unknown[]) => workflowsGetMock(...args),
        create: (...args: unknown[]) => workflowsCreateMock(...args),
      },
    },
    authedRawFetch: vi.fn().mockResolvedValue({ ok: true, text: () => Promise.resolve('') }),
  }
})

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: overrides.id ?? 'wf-1',
    name: overrides.name ?? 'Default',
    description: '',
    labels: [],
    transitions: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

describe('NewWorkflowModal dialog semantics (#350)', () => {
  beforeEach(() => {
    workflowsListMock.mockReset().mockResolvedValue([workflow()])
    workflowsGetMock.mockReset().mockResolvedValue(workflow())
    workflowsCreateMock.mockReset()
    useWorkflowStore.setState({ workflows: [], loading: false, selectedId: null })
  })

  async function openNewWorkflowModal() {
    render(<WorkflowPage />)
    const addButton = await screen.findByText('New Workflow')
    await userEvent.click(addButton)
    return screen.findByRole('dialog')
  }

  it('renders with dialog role and an accessible label', async () => {
    const dialog = await openNewWorkflowModal()
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAttribute('aria-label', 'New Workflow')
  })

  it('closes on Escape (previously had no Escape handling at all)', async () => {
    await openNewWorkflowModal()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
  })

  it('closes on backdrop click (previously had no backdrop dismissal at all)', async () => {
    const dialog = await openNewWorkflowModal()
    await userEvent.click(dialog)
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
  })

  it('focuses the name input on open', async () => {
    await openNewWorkflowModal()
    const nameInput = await screen.findByPlaceholderText('My Workflow')
    expect(nameInput).toHaveFocus()
  })
})
