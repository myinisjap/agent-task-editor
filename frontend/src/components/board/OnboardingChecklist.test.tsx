// OnboardingChecklist tests: per-step completion derived from repos /
// provider configs / agent configs / tasks, inline surfacing of failing
// /health/providers checks, dismissal persistence, and the
// all-steps-complete hidden state.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import OnboardingChecklist from './OnboardingChecklist'
import type { AgentConfig, ProviderCheck, ProviderConfig, Repo, Task } from '../../api/client'
import { useReposStore } from '../../stores/repos'
import { useTasksStore } from '../../stores/tasks'
import { useProviderConfigsStore } from '../../stores/providerConfigs'
import { useAgentsStore } from '../../stores/agents'

const reposListMock = vi.fn()
const tasksListMock = vi.fn()
const providerConfigsListMock = vi.fn()
const agentsListMock = vi.fn()
const healthProvidersMock = vi.fn()

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      repos: { list: (...args: unknown[]) => reposListMock(...args) },
      tasks: { list: (...args: unknown[]) => tasksListMock(...args) },
      providerConfigs: { list: (...args: unknown[]) => providerConfigsListMock(...args) },
      agents: { list: (...args: unknown[]) => agentsListMock(...args) },
      health: { providers: (...args: unknown[]) => healthProvidersMock(...args) },
    },
  }
})

function repo(overrides: Partial<Repo> = {}): Repo {
  return {
    id: 'r1',
    name: 'repo-one',
    path: '/repos/one',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as Repo
}

function providerConfig(overrides: Partial<ProviderConfig> = {}): ProviderConfig {
  return {
    id: 'pc1',
    name: 'anthropic',
    provider: 'claude',
    model: 'claude-sonnet',
    env_vars: '{}',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as ProviderConfig
}

function agentConfig(overrides: Partial<AgentConfig> = {}): AgentConfig {
  return {
    id: 'a1',
    name: 'planner',
    provider_config_id: 'pc1',
    enabled: true,
    system_prompt: '',
    labels: '[]',
    max_tokens: 1000,
    timeout_secs: 600,
    max_turns: 10,
    priority: 0,
    max_retries: 3,
    retry_backoff_secs: 30,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as AgentConfig
}

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'First task',
    description: '',
    type: 'feature',
    label: 'todo',
    repo_id: 'r1',
    workflow_id: 'wf',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as Task
}

function check(overrides: Partial<ProviderCheck> = {}): ProviderCheck {
  return {
    id: 'claude_cli',
    name: 'Claude CLI',
    status: 'ok',
    detail: 'Ready',
    ...overrides,
  }
}

function resetStores() {
  useReposStore.setState({ repos: [], loading: false, error: null })
  useTasksStore.setState({ tasks: [], loading: false, error: null })
  useProviderConfigsStore.setState({ configs: [], loading: false })
  useAgentsStore.setState({
    configs: [],
    loading: false,
    modelList: null,
    fetchingModels: false,
    claudeOptions: null,
  })
}

function renderChecklist() {
  return render(
    <MemoryRouter>
      <OnboardingChecklist />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  resetStores()
  try {
    localStorage.clear()
  } catch {
    // ignore
  }
  reposListMock.mockResolvedValue([])
  tasksListMock.mockResolvedValue({ items: [], nextCursor: null })
  providerConfigsListMock.mockResolvedValue([])
  agentsListMock.mockResolvedValue([])
  healthProvidersMock.mockResolvedValue({ checks: [] })
})

describe('OnboardingChecklist', () => {
  it('shows all four steps incomplete for a fresh instance', async () => {
    renderChecklist()

    expect(await screen.findByText('Get started')).toBeInTheDocument()
    expect(screen.getByText('Add a repo')).toBeInTheDocument()
    expect(screen.getByText('Configure a provider')).toBeInTheDocument()
    expect(screen.getByText('Create an agent config')).toBeInTheDocument()
    expect(screen.getByText('Create your first task')).toBeInTheDocument()
    // Incomplete rows render their destination link.
    expect(screen.getByText('Add a repo →')).toBeInTheDocument()
  })

  it('checks off the repo step once a repo exists, leaving the rest incomplete', async () => {
    reposListMock.mockResolvedValue([repo()])

    renderChecklist()

    await waitFor(() => {
      expect(screen.queryByText('Add a repo →')).not.toBeInTheDocument()
    })
    expect(screen.getByText('Configure a provider →')).toBeInTheDocument()
    expect(screen.getByText('Create an agent config →')).toBeInTheDocument()
  })

  it('treats an agent config with enabled:false as incomplete', async () => {
    reposListMock.mockResolvedValue([repo()])
    providerConfigsListMock.mockResolvedValue([providerConfig()])
    agentsListMock.mockResolvedValue([agentConfig({ enabled: false })])

    renderChecklist()

    await waitFor(() => {
      expect(screen.getByText('Create an agent config →')).toBeInTheDocument()
    })
  })

  it('renders nothing once all four steps are complete', async () => {
    reposListMock.mockResolvedValue([repo()])
    providerConfigsListMock.mockResolvedValue([providerConfig()])
    agentsListMock.mockResolvedValue([agentConfig({ enabled: true })])
    tasksListMock.mockResolvedValue({ items: [task()], nextCursor: null })

    const { container } = renderChecklist()

    await waitFor(() => {
      expect(container).toBeEmptyDOMElement()
    })
  })

  it('surfaces a failing readiness check inline under its step', async () => {
    healthProvidersMock.mockResolvedValue({
      checks: [
        check({
          id: 'claude_cli',
          status: 'error',
          detail: 'Claude CLI not authenticated',
          hint: 'Run `claude login`',
        }),
      ],
    })

    renderChecklist()

    expect(await screen.findByText('Claude CLI not authenticated')).toBeInTheDocument()
    expect(screen.getByText(/Run `claude login`/)).toBeInTheDocument()
  })

  it('dismisses permanently via the Dismiss button', async () => {
    const user = userEvent.setup()
    const { container } = renderChecklist()

    const dismissBtn = await screen.findByText('Dismiss')
    await user.click(dismissBtn)

    expect(container).toBeEmptyDOMElement()
    expect(localStorage.getItem('board.onboarding.dismissed')).toBe('true')
  })

  it('stays hidden on a fresh mount when dismissal was previously persisted', async () => {
    localStorage.setItem('board.onboarding.dismissed', 'true')

    const { container } = renderChecklist()

    await waitFor(() => {
      expect(container).toBeEmptyDOMElement()
    })
  })
})
