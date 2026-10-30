import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'


const m = vi.hoisted(() => ({ mobile: false }))
vi.mock('../lib/useIsMobile', () => ({ useIsMobile: () => m.mobile }))
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  const wf = (await import('../components/pipeline/testFixtures')).gateWorkflow()
  return {
    ...actual,
    api: {
      ...actual.api,
      workflows: { ...actual.api.workflows, list: async () => [wf], get: async () => wf },
      agents: { ...actual.api.agents, list: async () => [] },
      repos: { ...actual.api.repos, list: async () => [] },
      tasks: { ...actual.api.tasks, list: async () => ({ items: [], nextCursor: null, prevCursor: null }) },
      autonomy: {
        ...actual.api.autonomy,
        backtest: async () => (await import('../components/pipeline/testFixtures')).backtestResult,
        history: async () => ({ workflow_id: 'w1', label: 'review', current_mode: 'ask', changes: [], escapes: [], step_down: { escapes_counted: 0, threshold: 0, window_days: 14 } }),
        askRules: { list: async () => [], delete: async () => undefined },
        proposals: { ...actual.api.autonomy.proposals, list: async () => [], runs: async () => [] },
        settings: { ...actual.api.autonomy.settings, get: async () => ({ step_down_escapes: 2, step_down_window_days: 14, proposal_provider_config_id: null, proposal_interval_hours: 24 }) },
      },
      providerConfigs: { ...actual.api.providerConfigs, list: async () => [] },
    },
  }
})

import PipelineOverviewPage from './PipelineOverviewPage'
import { useWorkflowStore } from '../stores/workflow'


function renderAt(url = '/pipeline/overview') {
  return render(<MemoryRouter initialEntries={[url]}><PipelineOverviewPage /></MemoryRouter>)
}

describe('Pipeline gates', () => {
  beforeEach(() => {
    m.mobile = false
    useWorkflowStore.setState({ workflows: [], selectedId: null })
  })

  it('shows the dial chip on gate nodes instead of Human gate', async () => {
    renderAt()
    const node = await screen.findByTestId('pipeline-node-review')
    expect(node).toHaveTextContent('Always ask')
    expect(node).not.toHaveTextContent('Human gate')
  })

  it('clicking a gate node opens the gate panel, not the agent aside', async () => {
    renderAt()
    fireEvent.click(await screen.findByTestId('pipeline-node-review'))
    expect(await screen.findByRole('region', { name: /gate settings for review/i })).toBeInTheDocument()
    expect(screen.queryByRole('complementary', { name: /assign agents/i })).not.toBeInTheDocument()
  })

  it('opens the panel from ?gate= and removes the param', async () => {
    renderAt('/pipeline/overview?gate=review')
    expect(await screen.findByRole('region', { name: /gate settings for review/i })).toBeInTheDocument()
  })

  it('phones list gates and open a bottom sheet', async () => {
    m.mobile = true
    renderAt()
    const list = await screen.findByRole('list', { name: 'Gates' })
    expect(list.querySelectorAll('li')).toHaveLength(2)
    fireEvent.click(list.querySelectorAll('button')[1])
    expect(await screen.findByRole('dialog', { name: /gate settings: review/i })).toBeInTheDocument()
  })
})
