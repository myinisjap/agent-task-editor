import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import HomeRoute from './HomeRoute'
import { useReposStore } from '../../stores/repos'
import { useTasksStore } from '../../stores/tasks'
import { useProviderConfigsStore } from '../../stores/providerConfigs'
import { useAgentsStore } from '../../stores/agents'
import { useWorkflowStore } from '../../stores/workflow'
import { defaultWorkflow, agentFor } from '../../lib/__fixtures__/setupFixtures'

const m = {
  repos: vi.fn(), tasks: vi.fn(), providers: vi.fn(), agents: vi.fn(), workflows: vi.fn(),
}
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      repos: { list: () => m.repos() },
      tasks: { list: () => m.tasks() },
      providerConfigs: { list: () => m.providers() },
      agents: { list: () => m.agents() },
      workflows: { list: () => m.workflows() },
    },
  }
})

function renderHome(entry = '/') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/" element={<HomeRoute><div>DASH</div></HomeRoute>} />
        <Route path="/board" element={<div>BOARD</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  try { localStorage.clear() } catch { /* ignore */ }
  useReposStore.setState({ repos: [] })
  useTasksStore.setState({ tasks: [], loaded: false })
  useProviderConfigsStore.setState({ configs: [] })
  useAgentsStore.setState({ configs: [] })
  useWorkflowStore.setState({ workflows: [], selectedId: null })
  m.repos.mockResolvedValue([])
  m.tasks.mockResolvedValue({ items: [], nextCursor: null })
  m.providers.mockResolvedValue([])
  m.agents.mockResolvedValue([])
  m.workflows.mockResolvedValue([defaultWorkflow()])
})

describe('HomeRoute', () => {
  it('redirects to the Board while setup is incomplete', async () => {
    renderHome()
    expect(await screen.findByText('BOARD')).toBeInTheDocument()
  })

  it('stays on Home for review popup deep links while setup is incomplete', async () => {
    renderHome('/?task=t1')
    expect(await screen.findByText('DASH')).toBeInTheDocument()
  })

  it('shows the Dashboard once setup is complete', async () => {
    m.repos.mockResolvedValue([{ id: 'r' }])
    m.providers.mockResolvedValue([{ id: 'p' }])
    m.agents.mockResolvedValue(agentFor(['plan', 'work', 'testing', 'agent-review']))
    m.tasks.mockResolvedValue({ items: [{ id: 't' }], nextCursor: null })
    renderHome()
    expect(await screen.findByText('DASH')).toBeInTheDocument()
  })

  it('shows the Dashboard when the checklist was dismissed', async () => {
    localStorage.setItem('board.onboarding.dismissed', 'true')
    renderHome()
    expect(await screen.findByText('DASH')).toBeInTheDocument()
  })

  it('shows the Dashboard when tasks fail to load', async () => {
    m.tasks.mockRejectedValue(new Error('boom'))
    renderHome()
    expect(await screen.findByText('DASH')).toBeInTheDocument()
  })
})
