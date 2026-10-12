import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import type { AgentConfig, Workflow } from '../api/client'

const { listWorkflows, listAgents, updateAgent, getWorkflow, updateYaml, listTasks } = vi.hoisted(() => ({
  getWorkflow: vi.fn(),
  updateYaml: vi.fn(),
  listTasks: vi.fn(),
  listWorkflows: vi.fn(),
  listAgents: vi.fn(),
  updateAgent: vi.fn(),
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      workflows: { ...actual.api.workflows, list: listWorkflows, get: getWorkflow, updateYaml },
      tasks: { ...actual.api.tasks, list: listTasks },
      agents: { ...actual.api.agents, list: listAgents, update: updateAgent },
    },
  }
})

import PipelineOverviewPage from './PipelineOverviewPage'
import { useWorkflowStore } from '../stores/workflow'
import { useAgentsStore } from '../stores/agents'

const lbl = (name: string, i: number, extra: Record<string, unknown> = {}) =>
  ({ id: name, workflow_id: 'w1', name, color: '#6366f1', sort_order: i, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '', ...extra })
const tr = (from: string, to: string, trigger: string, path = 'success') =>
  ({ id: `${from}-${to}`, workflow_id: 'w1', from_label: from, to_label: to, trigger_type: trigger, path })

const wf = (id = 'w1', name = 'Main'): Workflow => ({
  id, name, description: '',
  labels: [
    lbl('work', 0), lbl('review', 1, { verify_mode: 'disabled' }), lbl('approve', 2, { agent_ignore: 1 }),
    lbl('done', 3, { is_terminal: 1, create_pr: 1 }),
  ],
  transitions: [tr('work', 'review', 'agent'), tr('review', 'approve', 'agent'), tr('approve', 'done', 'human')],
} as unknown as Workflow)

const agent = (id: string, name: string, labels: string[]): AgentConfig =>
  ({ id, name, labels: JSON.stringify(labels), enabled: true, provider_config_id: 'p', provider_config: { provider: 'claude' }, system_prompt: '' }) as unknown as AgentConfig

function renderPage() {
  return render(<MemoryRouter><PipelineOverviewPage /></MemoryRouter>)
}

describe('PipelineOverviewPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listWorkflows.mockResolvedValue([wf()])
    listAgents.mockResolvedValue([agent('a1', 'Coder', ['work']), agent('a2', 'Reviewer', [])])
    getWorkflow.mockResolvedValue(wf())
    updateYaml.mockResolvedValue(wf())
    listTasks.mockResolvedValue({ items: [], nextCursor: null, prevCursor: null })
    updateAgent.mockImplementation(async (_id: string, body: AgentConfig) => ({ config: body }))
    useWorkflowStore.setState({ workflows: [], selectedId: null })
    useAgentsStore.setState({ configs: [], loaded: false })
  })

  it('renders a node per label with staffing and markers', async () => {
    renderPage()
    await screen.findByTestId('pipeline-node-work')
    expect(screen.getByTestId('pipeline-node-work')).toHaveTextContent('Coder · claude')
    expect(screen.getByTestId('pipeline-node-review')).toHaveTextContent('No agent assigned')
    expect(screen.getByTestId('pipeline-node-review')).toHaveTextContent('verify: disabled')
    expect(screen.getByTestId('pipeline-node-approve')).toHaveTextContent('Human gate')
    expect(screen.getByTestId('pipeline-node-done')).toHaveTextContent('opens PR')
    expect(screen.getByTestId('pipeline-node-done')).toHaveTextContent('terminal')
  })

  it('assigning an agent from the panel updates its labels', async () => {
    const user = userEvent.setup()
    renderPage()
    // fireEvent: user-event's mousedown trips d3-zoom's pan handler in jsdom.
    fireEvent.click(await screen.findByTestId('pipeline-node-review'))
    const panel = screen.getByRole('complementary', { name: /assign agents to review/i })
    await user.click(panel.querySelector('input[type=checkbox]:not(:checked)') as HTMLElement)
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1))
    const [id, body] = updateAgent.mock.calls[0]
    expect(id).toBe('a1')
    expect(JSON.parse(body.labels)).toEqual(['work', 'review'])
  })

  it('unassigning removes the label', async () => {
    const user = userEvent.setup()
    renderPage()
    fireEvent.click(await screen.findByTestId('pipeline-node-work'))
    await user.click(screen.getByRole('checkbox', { name: /coder/i }))
    await waitFor(() => expect(updateAgent).toHaveBeenCalled())
    expect(JSON.parse(updateAgent.mock.calls[0][1].labels)).toEqual([])
  })

  it('shows a workflow selector only when there are several workflows', async () => {
    renderPage()
    await screen.findByTestId('pipeline-node-work')
    expect(screen.queryByLabelText('Workflow')).not.toBeInTheDocument()
  })

  it('switches workflows from the selector', async () => {
    listWorkflows.mockResolvedValue([wf('w1', 'Main'), { ...wf('w2', 'Hotfix'), labels: [lbl('only', 0)] , transitions: [] } as unknown as Workflow])
    const user = userEvent.setup()
    renderPage()
    await user.selectOptions(await screen.findByLabelText('Workflow'), 'w2')
    expect(await screen.findByTestId('pipeline-node-only')).toBeInTheDocument()
  })

  describe('edit mode', () => {
    const edit = async () => {
      const user = userEvent.setup()
      renderPage()
      await screen.findByTestId('pipeline-node-work')
      await user.click(screen.getByRole('button', { name: 'Edit workflow' }))
      return user
    }
    const addLabelNamed = async (user: ReturnType<typeof userEvent.setup>, name: string) => {
      await user.type(screen.getByLabelText('New label name'), name)
      await user.click(screen.getByRole('button', { name: 'Add label' }))
    }

    it('starts clean, and Save is disabled until something changes', async () => {
      await edit()
      expect(screen.queryByTestId('dirty-indicator')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    })

    it('adds a label, connects it without dragging, and saves the expected payload', async () => {
      const user = await edit()
      await addLabelNamed(user, 'qa')
      expect(await screen.findByTestId('pipeline-node-qa')).toBeInTheDocument()
      expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument()

      await user.selectOptions(screen.getByLabelText('Transition from'), 'review')
      await user.selectOptions(screen.getByLabelText('Transition to'), 'qa')
      await user.selectOptions(screen.getByLabelText('Transition type'), 'human')
      await user.click(screen.getByRole('button', { name: 'Add transition' }))

      await user.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(updateYaml).toHaveBeenCalledTimes(1))
      const [id, body] = updateYaml.mock.calls[0]
      expect(id).toBe('w1')
      const doc = JSON.parse(body)
      expect(doc.labels.map((l: { name: string }) => l.name)).toEqual(['work', 'review', 'approve', 'done', 'qa'])
      expect(doc.labels[3]).toMatchObject({ is_terminal: true, create_pr: true })
      expect(doc.labels[1]).toMatchObject({ verify_mode: 'disabled' })
      expect(doc.transitions).toContainEqual({ from: 'review', to: 'qa', trigger: 'human' })
      expect(doc.transitions).toHaveLength(4)
      // Leaves edit mode after a successful save.
      expect(await screen.findByRole('button', { name: 'Edit workflow' })).toBeInTheDocument()
    })

    it('rejects a duplicate label name inline', async () => {
      const user = await edit()
      await addLabelNamed(user, 'work')
      expect(screen.getByRole('alert')).toHaveTextContent(/already exists/)
      expect(screen.queryByTestId('dirty-indicator')).not.toBeInTheDocument()
    })

    it('discard restores the original graph', async () => {
      const user = await edit()
      await addLabelNamed(user, 'qa')
      await screen.findByTestId('pipeline-node-qa')
      await user.click(screen.getByRole('button', { name: 'Discard' }))
      expect(screen.queryByTestId('pipeline-node-qa')).not.toBeInTheDocument()
      expect(updateYaml).not.toHaveBeenCalled()
      expect(screen.getByRole('button', { name: 'Edit workflow' })).toBeInTheDocument()
    })

    it('deletes a transition and toggles agent / terminal / verify settings', async () => {
      const user = await edit()
      await user.click(screen.getByRole('button', { name: /delete transition work to review/i }))
      fireEvent.click(screen.getByTestId('pipeline-node-review'))
      const panel = screen.getByRole('region', { name: /edit label review/i })
      await user.click(within(panel).getByRole('checkbox', { name: /agents work this label/i }))
      await user.selectOptions(within(panel).getByLabelText('Verify'), 'custom')
      // custom verify without a command blocks saving
      expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
      await user.type(within(panel).getByLabelText('Verify command'), 'npm test')
      await user.type(within(panel).getByLabelText('WIP limit'), '2')
      await user.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(updateYaml).toHaveBeenCalled())
      const doc = JSON.parse(updateYaml.mock.calls[0][1])
      expect(doc.transitions.map((t: { from: string }) => t.from)).toEqual(['review', 'approve'])
      expect(doc.labels[1]).toMatchObject({ agent_ignore: true, verify_mode: 'custom', verify_command: 'npm test', wip_limit: 2 })
    })

    it('sets a human gate on a label', async () => {
      const user = await edit()
      fireEvent.click(screen.getByTestId('pipeline-node-work'))
      const panel = screen.getByRole('region', { name: /edit label work/i })
      await user.click(within(panel).getByRole('checkbox', { name: /human approval gate/i }))
      await user.click(screen.getByRole('button', { name: 'Save' }))
      await waitFor(() => expect(updateYaml).toHaveBeenCalled())
      const doc = JSON.parse(updateYaml.mock.calls[0][1])
      expect(doc.transitions[0]).toEqual({ from: 'work', to: 'review', trigger: 'human' })
    })

    it('blocks deleting or renaming a label that has tasks', async () => {
      listTasks.mockResolvedValue({ items: [{ id: 't', workflow_id: 'w1', label: 'work' }], nextCursor: null, prevCursor: null })
      const user = await edit()
      fireEvent.click(screen.getByTestId('pipeline-node-work'))
      const panel = await screen.findByRole('region', { name: /edit label work/i })
      await waitFor(() => expect(within(panel).getByRole('button', { name: /delete label/i })).toBeDisabled())
      expect(panel).toHaveTextContent('1 task on this label')
      expect(within(panel).getByRole('button', { name: 'Rename' })).toBeDisabled()
      // an unrelated label is free to delete
      fireEvent.click(screen.getByTestId('pipeline-node-approve'))
      const other = screen.getByRole('region', { name: /edit label approve/i })
      await waitFor(() => expect(within(other).getByRole('button', { name: /delete label/i })).toBeEnabled())
      await user.click(within(other).getByRole('button', { name: /delete label/i }))
      expect(screen.queryByTestId('pipeline-node-approve')).not.toBeInTheDocument()
    })

    it('surfaces a concurrent change instead of overwriting, and can overwrite on request', async () => {
      getWorkflow.mockResolvedValue({ ...wf(), updated_at: 'newer' })
      const user = await edit()
      await addLabelNamed(user, 'qa')
      await user.click(screen.getByRole('button', { name: 'Save' }))
      expect(await screen.findByText(/changed on the server/i)).toBeInTheDocument()
      expect(updateYaml).not.toHaveBeenCalled()
      await user.click(screen.getByRole('button', { name: /overwrite with my edits/i }))
      await waitFor(() => expect(updateYaml).toHaveBeenCalledTimes(1))
    })

    it('shows server errors inline and stays in edit mode', async () => {
      updateYaml.mockRejectedValue(new Error('label "qa": boom'))
      const user = await edit()
      await addLabelNamed(user, 'qa')
      await user.click(screen.getByRole('button', { name: 'Save' }))
      expect(await screen.findByText('label "qa": boom')).toBeInTheDocument()
      expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument()
    })
  })
})
