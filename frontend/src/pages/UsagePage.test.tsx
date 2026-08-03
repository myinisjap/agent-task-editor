// UsagePage global-cost-ceiling tests: verifies the "Global spend ceiling"
// section (daily/monthly progress bars + burn-rate forecast) and the
// cost-by-repo table render from the dashboard payload's global_cost_budget
// / cost_by_repo fields, and stay hidden when those fields are absent
// (no cap configured / no repo spend recorded).
import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import UsagePage from './UsagePage'

const dashboardGetMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      dashboard: { get: (...args: unknown[]) => dashboardGetMock(...args) },
    },
  }
})

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn(() => () => {}),
  },
}))

function renderPage() {
  return render(
    <MemoryRouter>
      <UsagePage />
    </MemoryRouter>
  )
}

describe('UsagePage global spend ceiling', () => {
  it('renders daily and monthly progress bars with the forecast when a cap is configured', async () => {
    dashboardGetMock.mockResolvedValue({
      label_counts: {},
      active_agents: [],
      intervention_queue: [],
      cost_total: { input_tokens: 0, output_tokens: 0, cost_usd: 0 },
      cost_by_provider: [],
      agent_config_stats: [],
      cost_by_day: [],
      cost_by_task: [],
      cost_by_repo: [],
      claude_usage: { available: false, five_hour_percent: 0, weekly_percent: 0 },
      repo_concurrency: [],
      global_cost_budget: {
        daily_limit_usd: 10,
        monthly_limit_usd: 100,
        daily_spent_usd: 3.5,
        monthly_spent_usd: 3.5,
        tripped: false,
        daily_forecast_usd: 5.25,
        monthly_forecast_usd: 42,
      },
    })

    renderPage()

    await waitFor(() => expect(screen.getByText('Global spend ceiling')).toBeInTheDocument())
    expect(screen.getByText('Today')).toBeInTheDocument()
    expect(screen.getByText('This month')).toBeInTheDocument()
    expect(screen.getByText('$3.50 / $10.00')).toBeInTheDocument()
    expect(screen.getByText('$3.50 / $100.00')).toBeInTheDocument()
    expect(screen.getByText(/Projected at current burn: \$5.25/)).toBeInTheDocument()
    expect(screen.queryByText(/Dispatch is halted/)).not.toBeInTheDocument()
  })

  it('surfaces the tripped banner when the cap has been reached', async () => {
    dashboardGetMock.mockResolvedValue({
      label_counts: {},
      active_agents: [],
      intervention_queue: [],
      cost_total: { input_tokens: 0, output_tokens: 0, cost_usd: 0 },
      cost_by_provider: [],
      agent_config_stats: [],
      cost_by_day: [],
      cost_by_task: [],
      cost_by_repo: [],
      claude_usage: { available: false, five_hour_percent: 0, weekly_percent: 0 },
      repo_concurrency: [],
      global_cost_budget: {
        daily_limit_usd: 10,
        monthly_limit_usd: 0,
        daily_spent_usd: 12,
        monthly_spent_usd: 0,
        tripped: true,
        tripped_reason: 'daily',
        daily_forecast_usd: 12,
      },
    })

    renderPage()

    await waitFor(() => expect(screen.getByText(/Dispatch is halted/)).toBeInTheDocument())
    expect(screen.getByText(/the daily spend cap has been reached/)).toBeInTheDocument()
  })

  it('omits the global spend ceiling section entirely when no cap is configured', async () => {
    dashboardGetMock.mockResolvedValue({
      label_counts: {},
      active_agents: [],
      intervention_queue: [],
      cost_total: { input_tokens: 0, output_tokens: 0, cost_usd: 0 },
      cost_by_provider: [],
      agent_config_stats: [],
      cost_by_day: [],
      cost_by_task: [],
      cost_by_repo: [],
      claude_usage: { available: false, five_hour_percent: 0, weekly_percent: 0 },
      repo_concurrency: [],
    })

    renderPage()

    await waitFor(() => expect(screen.getByText('No usage data yet.')).toBeInTheDocument())
    expect(screen.queryByText('Global spend ceiling')).not.toBeInTheDocument()
  })

  it('renders the cost-by-repo table', async () => {
    dashboardGetMock.mockResolvedValue({
      label_counts: {},
      active_agents: [],
      intervention_queue: [],
      cost_total: { input_tokens: 100, output_tokens: 50, cost_usd: 1.5 },
      cost_by_provider: [],
      agent_config_stats: [],
      cost_by_day: [],
      cost_by_task: [],
      cost_by_repo: [
        { repo_id: 'r1', repo_name: 'my-repo', input_tokens: 100, output_tokens: 50, cost_usd: 1.5, run_count: 3 },
      ],
      claude_usage: { available: false, five_hour_percent: 0, weekly_percent: 0 },
      repo_concurrency: [],
    })

    renderPage()

    await waitFor(() => expect(screen.getByText('Cost by repo')).toBeInTheDocument())
    expect(screen.getByText('my-repo')).toBeInTheDocument()
  })
})
