import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import IntakeRulesPage from './IntakeRulesPage'
import type { IntakeRule, Repo, TaskTemplate, Workflow } from '../api/client'

const listMock = vi.fn()
const createMock = vi.fn()
const updateMock = vi.fn()
const deleteMock = vi.fn()
const previewMock = vi.fn()
const reposListMock = vi.fn()
const templatesListMock = vi.fn()
const workflowsListMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      intakeRules: {
        list: (...args: unknown[]) => listMock(...args),
        get: vi.fn(),
        create: (...args: unknown[]) => createMock(...args),
        update: (...args: unknown[]) => updateMock(...args),
        delete: (...args: unknown[]) => deleteMock(...args),
        preview: (...args: unknown[]) => previewMock(...args),
      },
      repos: { ...actual.api.repos, list: (...args: unknown[]) => reposListMock(...args) },
      templates: { ...actual.api.templates, list: (...args: unknown[]) => templatesListMock(...args) },
      workflows: { ...actual.api.workflows, list: (...args: unknown[]) => workflowsListMock(...args) },
    },
  }
})

function repo(overrides: Partial<Repo> = {}): Repo {
  return {
    id: 'repo-1',
    name: 'acme/widgets',
    path: '/tmp/widgets',
    workflow_id: 'wf-1',
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf-1',
    name: 'Default',
    description: '',
    labels: [
      { id: 'l1', workflow_id: 'wf-1', name: 'not_ready', color: '#333', sort_order: 0, agent_ignore: 1, is_terminal: 0, create_pr: 0, wip_limit_hard: 0 },
      { id: 'l2', workflow_id: 'wf-1', name: 'work', color: '#333', sort_order: 1, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit_hard: 0 },
    ],
    transitions: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

function rule(overrides: Partial<IntakeRule> = {}): IntakeRule {
  return {
    id: 'rule-1',
    name: 'Bug triage',
    enabled: true,
    sort_order: 0,
    match_source: 'issue',
    match_repo_id: null,
    match_labels: ['bug'],
    match_title_pattern: '',
    match_body_pattern: '',
    match_author_assoc: [],
    apply_template_id: null,
    apply_priority: null,
    apply_target_label: '',
    apply_workflow_id: null,
    apply_max_cost_usd: null,
    stop_processing: true,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

describe('IntakeRulesPage', () => {
  beforeEach(() => {
    listMock.mockReset().mockResolvedValue([rule()])
    reposListMock.mockReset().mockResolvedValue([repo()])
    templatesListMock.mockReset().mockResolvedValue([] as TaskTemplate[])
    workflowsListMock.mockReset().mockResolvedValue([workflow()])
    createMock.mockReset()
    updateMock.mockReset()
    deleteMock.mockReset()
    previewMock.mockReset().mockResolvedValue({ matches: [] })
  })

  it('renders the existing rules list', async () => {
    render(<IntakeRulesPage />)
    expect(await screen.findByText('Bug triage')).toBeInTheDocument()
  })

  it('creates a new rule via the form', async () => {
    createMock.mockResolvedValue(rule({ id: 'rule-2', name: 'New rule' }))
    render(<IntakeRulesPage />)
    await screen.findByText('Bug triage')

    await userEvent.click(screen.getByText('+ Add Rule'))
    const nameInputs = screen.getAllByPlaceholderText('Bug triage')
    await userEvent.type(nameInputs[0], 'New rule')
    await userEvent.click(screen.getByText('Add Rule'))

    await waitFor(() => expect(createMock).toHaveBeenCalled())
    const body = createMock.mock.calls[0][0]
    expect(body.name).toBe('New rule')
  })

  it('blocks submission when an agent-triggerable target label lacks a trusted author constraint', async () => {
    render(<IntakeRulesPage />)
    await screen.findByText('Bug triage')

    await userEvent.click(screen.getByText('+ Add Rule'))
    const nameInputs = screen.getAllByPlaceholderText('Bug triage')
    await userEvent.type(nameInputs[0], 'unsafe rule')

    // Select the repo so the workflow's labels resolve.
    const repoSelects = screen.getAllByDisplayValue('Any repo')
    await userEvent.selectOptions(repoSelects[0], 'repo-1')

    // Target the agent-triggerable label without any author constraint.
    const targetInputs = screen.getAllByPlaceholderText('Leave default (human-gate label)')
    await userEvent.type(targetInputs[0], 'work')

    expect(await screen.findByText(/Auto-start warning/i)).toBeInTheDocument()
    expect(screen.getByText('Add Rule')).toBeDisabled()

    // Restricting to a trusted association clears the warning and enables submit.
    const ownerCheckboxes = screen.getAllByLabelText(/^OWNER$/)
    await userEvent.click(ownerCheckboxes[0])

    await waitFor(() => expect(screen.queryByText(/Auto-start warning/i)).not.toBeInTheDocument())
    expect(screen.getByText('Add Rule')).not.toBeDisabled()
  })

  it('does not require a trusted author constraint for a schedule-source target label', async () => {
    render(<IntakeRulesPage />)
    await screen.findByText('Bug triage')

    await userEvent.click(screen.getByText('+ Add Rule'))
    const nameInputs = screen.getAllByPlaceholderText('Bug triage')
    await userEvent.type(nameInputs[0], 'schedule rule')

    // Switch source to Schedule — the auto-start gate (which exists to
    // protect against untrusted imported issue content) should not apply,
    // since a schedule firing has no author to check.
    const sourceSelects = screen.getAllByDisplayValue('Issue import')
    await userEvent.selectOptions(sourceSelects[0], 'schedule')

    const repoSelects = screen.getAllByDisplayValue('Any repo')
    await userEvent.selectOptions(repoSelects[0], 'repo-1')

    const targetInputs = screen.getAllByPlaceholderText('Leave default (human-gate label)')
    await userEvent.type(targetInputs[0], 'work')

    expect(screen.queryByText(/Auto-start warning/i)).not.toBeInTheDocument()
    expect(screen.getByText('Add Rule')).not.toBeDisabled()
  })

  it('warns and blocks submission when a schedule-source rule sets a template', async () => {
    templatesListMock.mockResolvedValue([
      { id: 'tmpl-1', name: 'Triage', title: 'Triage', description: '', type: 'bug', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
    ] as TaskTemplate[])
    render(<IntakeRulesPage />)
    await screen.findByText('Bug triage')

    await userEvent.click(screen.getByText('+ Add Rule'))
    const nameInputs = screen.getAllByPlaceholderText('Bug triage')
    await userEvent.type(nameInputs[0], 'schedule with template')

    const sourceSelects = screen.getAllByDisplayValue('Issue import')
    await userEvent.selectOptions(sourceSelects[0], 'schedule')

    // The template select should now be disabled for a schedule-source rule.
    const templateSelects = await screen.findAllByDisplayValue('None')
    const templateSelect = templateSelects.find((el) => el.tagName === 'SELECT') as HTMLSelectElement
    expect(templateSelect).toBeDisabled()
  })

  it('deletes a rule', async () => {
    deleteMock.mockResolvedValue(undefined)
    window.confirm = vi.fn().mockReturnValue(true)
    render(<IntakeRulesPage />)
    await screen.findByText('Bug triage')

    await userEvent.click(screen.getByText('Delete'))
    await waitFor(() => expect(deleteMock).toHaveBeenCalledWith('rule-1'))
  })
})
