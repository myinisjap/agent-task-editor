// issue #350 — the "New Workflow" modal used to be a hand-rolled overlay
// with only a Cancel button: no dialog role, no Escape, and no
// backdrop-click dismissal at all. It's now built on the shared ModalShell.
//
// issue #332 — loadWorkflow() fired two uncancelled/unguarded fetches per
// workflow switch, so a slow response for workflow A could land after the
// user switched to workflow B and overwrite B's YAML / flowchart / error
// banner (and then Save would write A's YAML onto B). The tests below cover
// the request-sequencing fix.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import WorkflowPage from './WorkflowPage'
import type { Workflow } from '../api/client'
import { useWorkflowStore } from '../stores/workflow'

const workflowsListMock = vi.fn()
const workflowsGetMock = vi.fn()
const workflowsCreateMock = vi.fn()
const workflowsUpdateYamlMock = vi.fn()
const authedRawFetchMock = vi.fn()

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
        updateYaml: (...args: unknown[]) => workflowsUpdateYamlMock(...args),
      },
    },
    authedRawFetch: (...args: unknown[]) => authedRawFetchMock(...args),
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
    workflowsUpdateYamlMock.mockReset()
    authedRawFetchMock.mockReset().mockResolvedValue({ ok: true, text: () => Promise.resolve('') })
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

describe('stale load responses (#332)', () => {
  const WF_A = workflow({ id: 'wf-a', name: 'Workflow A' })
  const WF_B = workflow({ id: 'wf-b', name: 'Workflow B' })

  // Minimal, independently valid YAML for each workflow: a start label
  // reachable chain ending in a terminal label with no outgoing transitions
  // (mirrors the FULL_WORKFLOW fixture in parseWorkflowYaml.test.ts).
  const YAML_A = `name: Workflow A\nlabels:\n  - name: todo\n    sort_order: 0\n  - name: done\n    sort_order: 1\n    is_terminal: true\ntransitions:\n  - from: todo\n    to: done\n`
  const YAML_B = `name: Workflow B\nlabels:\n  - name: backlog\n    sort_order: 0\n  - name: shipped\n    sort_order: 1\n    is_terminal: true\ntransitions:\n  - from: backlog\n    to: shipped\n`

  // A deferred promise so the test controls exactly when workflow A's
  // export.yaml response resolves, relative to workflow B's.
  let resolveAExport: (r: { ok: boolean; status?: number; text: () => Promise<string> }) => void
  let aExportPromise: Promise<{ ok: boolean; status?: number; text: () => Promise<string> }>

  beforeEach(() => {
    workflowsListMock.mockReset().mockResolvedValue([WF_A, WF_B])
    workflowsGetMock.mockReset().mockImplementation((id: string) =>
      Promise.resolve(id === 'wf-a' ? WF_A : WF_B),
    )
    workflowsCreateMock.mockReset()
    workflowsUpdateYamlMock.mockReset().mockResolvedValue(WF_B)

    aExportPromise = new Promise((resolve) => { resolveAExport = resolve })
    authedRawFetchMock.mockReset().mockImplementation((url: string) => {
      if (url.includes('wf-a')) return aExportPromise
      if (url.includes('wf-b')) return Promise.resolve({ ok: true, text: () => Promise.resolve(YAML_B) })
      return Promise.resolve({ ok: true, text: () => Promise.resolve('') })
    })

    useWorkflowStore.setState({ workflows: [], loading: false, selectedId: null })
  })

  async function selectAThenB() {
    render(<WorkflowPage />)
    const tabA = await screen.findByText('Workflow A')
    const tabB = await screen.findByText('Workflow B')

    await userEvent.click(tabA) // starts A's export fetch (deferred)
    await userEvent.click(tabB) // starts + resolves B's export fetch

    // Wait for B's YAML to land in the textarea.
    await waitFor(() => {
      const textarea = screen.getByPlaceholderText('Enter YAML…') as HTMLTextAreaElement
      expect(textarea.value).toBe(YAML_B)
    })
  }

  it('a late response for a previously-selected workflow does not overwrite the editor', async () => {
    await selectAThenB()

    // Now let A's (stale) export resolve.
    resolveAExport({ ok: true, text: () => Promise.resolve(YAML_A) })

    // Give any stray microtasks a chance to run, then assert B's YAML is
    // still shown.
    await new Promise((r) => setTimeout(r, 10))
    const textarea = screen.getByPlaceholderText('Enter YAML…') as HTMLTextAreaElement
    expect(textarea.value).toBe(YAML_B)
  })

  it('Save never submits a stale workflow YAML to the wrong id', async () => {
    await selectAThenB()
    resolveAExport({ ok: true, text: () => Promise.resolve(YAML_A) })
    await new Promise((r) => setTimeout(r, 10))

    const saveButton = await screen.findByText('Save')
    await userEvent.click(saveButton)

    await waitFor(() => {
      expect(workflowsUpdateYamlMock).toHaveBeenCalled()
    })
    expect(workflowsUpdateYamlMock).toHaveBeenCalledWith('wf-b', YAML_B)
    expect(workflowsUpdateYamlMock).not.toHaveBeenCalledWith('wf-b', YAML_A)
  })

  it('a late failure for a previously-selected workflow does not show an error banner', async () => {
    await selectAThenB()

    resolveAExport({ ok: false, status: 500, text: () => Promise.resolve('') })
    await new Promise((r) => setTimeout(r, 10))

    expect(screen.queryByText(/Failed to load workflow YAML/)).not.toBeInTheDocument()
  })
})
