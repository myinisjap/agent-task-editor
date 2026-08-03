// AgentPerformancePage tests: the outcome-quality section leads with
// cost-to-done/rework and re-fetches when the repo filter changes; the
// legacy agent_config_stats table still renders success rate underneath.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AgentPerformancePage from './AgentPerformancePage'
import type { Dashboard, OutcomeQuality, Repo } from '../api/client'

const dashboardGetMock = vi.fn()
const outcomeQualityMock = vi.fn()
const reposListMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      dashboard: {
        get: (...args: unknown[]) => dashboardGetMock(...args),
        outcomeQuality: (...args: unknown[]) => outcomeQualityMock(...args),
      },
      repos: { list: (...args: unknown[]) => reposListMock(...args) },
    },
  }
})

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn(() => () => {}),
  },
}))

const dashboard: Dashboard = {
  label_counts: {},
  active_agents: [],
  intervention_queue: [],
  cost_total: { input_tokens: 0, output_tokens: 0, cost_usd: 0 },
  cost_by_provider: [],
  agent_config_stats: [
    {
      agent_config_id: 'cfg-1',
      agent_name: 'worker',
      provider: 'claude',
      run_count: 10,
      completed_count: 8,
      failed_count: 1,
      waiting_human_count: 1,
      success_rate_percent: 80,
      avg_duration_secs: 120,
      p90_duration_secs: 200,
      avg_turns_to_done: 1.2,
      avg_transient_retries: 0,
      tasks_with_retries: 0,
      input_tokens: 1000,
      output_tokens: 500,
      cost_usd: 1.23,
    },
  ],
  cost_by_day: [],
  cost_by_task: [],
  cost_by_repo: [],
  claude_usage: { available: false, five_hour_percent: 0, weekly_percent: 0 },
  repo_concurrency: [],
}

const outcomeQuality: OutcomeQuality = {
  configs: [
    {
      agent_config_id: 'cfg-1',
      agent_name: 'worker',
      provider: 'claude',
      tasks_done: 20,
      avg_cost_to_done_usd: 0.5,
      rework_rate_percent: 15,
      rework_n: 20,
      low_sample_rework: false,
      human_touch_rate_percent: 5,
      human_touch_n: 20,
      low_sample_human_touch: false,
      avg_review_comments: 1.5,
      runs_finished: 22,
      escalation_rate_percent: 4.5,
      low_sample_escalation: false,
    },
  ],
}

const repos: Repo[] = [
  { id: 'repo-1', name: 'repo-one', path: '/tmp/repo-one', created_at: '2026-01-01T00:00:00Z' } as Repo,
]

beforeEach(() => {
  dashboardGetMock.mockReset().mockResolvedValue(dashboard)
  outcomeQualityMock.mockReset().mockResolvedValue(outcomeQuality)
  reposListMock.mockReset().mockResolvedValue(repos)
})

describe('AgentPerformancePage', () => {
  it('renders outcome-quality metrics ahead of the legacy success-rate table', async () => {
    render(<AgentPerformancePage />)

    await waitFor(() => expect(screen.getAllByText('worker').length).toBeGreaterThan(0))

    // Outcome-quality section: cost-to-done, rework rate, tasks done.
    expect(screen.getByText('$0.5000')).toBeInTheDocument()
    expect(screen.getByText('20')).toBeInTheDocument()
    expect(screen.getByText('15%')).toBeInTheDocument()

    // Legacy success-rate table still renders underneath.
    expect(screen.getByText('80%')).toBeInTheDocument()

    // Outcome quality's heading appears before the legacy section's.
    const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(headings.indexOf('Outcome quality')).toBeLessThan(headings.indexOf('Agent config performance'))
  })

  it('refetches outcome quality scoped to the selected repo', async () => {
    const user = userEvent.setup()
    render(<AgentPerformancePage />)

    await waitFor(() => expect(reposListMock).toHaveBeenCalled())
    await waitFor(() => expect(outcomeQualityMock).toHaveBeenCalledWith(undefined))

    const select = await screen.findByDisplayValue('All repos')
    await user.selectOptions(select, 'repo-1')

    await waitFor(() => expect(outcomeQualityMock).toHaveBeenCalledWith('repo-1'))
  })

  it('greys out low-sample rates instead of hiding them', async () => {
    outcomeQualityMock.mockResolvedValue({
      configs: [
        {
          ...outcomeQuality.configs[0],
          tasks_done: 2,
          rework_rate_percent: 100,
          rework_n: 2,
          low_sample_rework: true,
        },
      ],
    })

    render(<AgentPerformancePage />)

    await waitFor(() => expect(screen.getByText('100%')).toBeInTheDocument())
    expect(screen.getByText('(n=2)')).toBeInTheDocument()
  })
})
