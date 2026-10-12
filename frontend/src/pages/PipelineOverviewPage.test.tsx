import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import type { AgentConfig, Workflow } from '../api/client'

const { listWorkflows, listAgents, updateAgent } = vi.hoisted(() => ({
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
      workflows: { ...actual.api.workflows, list: listWorkflows },
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
})
