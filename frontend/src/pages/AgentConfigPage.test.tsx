// AgentConfigPage opens on an existing agent (or ?agent=<id>) rather than an
// empty "New Agent Config" form; New and the zero-agent state show the form.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import AgentConfigPage from './AgentConfigPage'
import { useAgentsStore } from '../stores/agents'
import { useWorkflowStore } from '../stores/workflow'
import { useProviderConfigsStore } from '../stores/providerConfigs'
import type { AgentConfig } from '../api/client'

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      skills: { list: vi.fn().mockResolvedValue([]) },
      agents: { skills: vi.fn().mockResolvedValue([]), versions: vi.fn().mockResolvedValue([]), list: vi.fn().mockResolvedValue([]) },
    },
  }
})

const mk = (id: string, name: string): AgentConfig =>
  ({
    id, name, provider_config_id: '', fallback_provider_config_ids: [], escalation_ladder: [], system_prompt: '', labels: '[]',
    max_tokens: 1, timeout_secs: 1, max_turns: 1, max_retries: 0, retry_backoff_secs: 0, enabled: true,
  }) as unknown as AgentConfig

function setup(agents: AgentConfig[], url = '/pipeline/agents') {
  const noop = vi.fn().mockResolvedValue(undefined)
  useAgentsStore.setState({ configs: agents, fetch: noop, fetchClaudeOptions: noop })
  useWorkflowStore.setState({ workflows: [], fetch: noop })
  useProviderConfigsStore.setState({ configs: [], fetch: noop })
  return render(<MemoryRouter initialEntries={[url]}><AgentConfigPage /></MemoryRouter>)
}

beforeEach(() => vi.clearAllMocks())

describe('AgentConfigPage', () => {
  it('opens on the first agent', async () => {
    setup([mk('a1', 'alpha'), mk('a2', 'beta')])
    expect(await screen.findByText('Edit: alpha')).toBeInTheDocument()
  })

  it('selects the agent from ?agent=', async () => {
    setup([mk('a1', 'alpha'), mk('a2', 'beta')], '/pipeline/agents?agent=a2')
    expect(await screen.findByText('Edit: beta')).toBeInTheDocument()
  })

  it('shows the empty form after clicking New', async () => {
    setup([mk('a1', 'alpha')])
    await screen.findByText('Edit: alpha')
    await userEvent.click(screen.getByRole('button', { name: '+ New' }))
    await waitFor(() => expect(screen.getByText('New Agent Config')).toBeInTheDocument())
  })

  it('shows the New form when there are no agents', async () => {
    setup([])
    expect(await screen.findByText('New Agent Config')).toBeInTheDocument()
  })
})
