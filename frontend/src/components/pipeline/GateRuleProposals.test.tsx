import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import type { RuleProposal, RuleProposalRun } from '../../api/client'

const m = vi.hoisted(() => ({
  list: vi.fn(), runs: vi.fn(), run: vi.fn(), accept: vi.fn(), dismiss: vi.fn(),
  getSettings: vi.fn(), updateSettings: vi.fn(), providers: vi.fn(),
}))

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      providerConfigs: { ...actual.api.providerConfigs, list: m.providers },
      autonomy: {
        ...actual.api.autonomy,
        proposals: { list: m.list, runs: m.runs, run: m.run, accept: m.accept, dismiss: m.dismiss },
        settings: { get: m.getSettings, update: m.updateSettings },
      },
    },
  }
})

import GateRuleProposals from './GateRuleProposals'

const baseSettings = { step_down_escapes: 2, step_down_window_days: 14, proposal_provider_config_id: null, proposal_interval_hours: 24 }

const proposal: RuleProposal = {
  id: 'p1', workflow_id: 'w1', label: 'review', repo_id: null, path_globs: ['*.sql'], task_type: null,
  min_changed_lines: null, summary: 'files match *.sql; all repos', rationale: 'three migrations broke', evidence: [
    { task_id: 't1', title: 'Add index', kind: 'escape', source: 'undo' },
  ], caught: 3, evidence_total: 4, cost: 1, clean_approvals: 20, unknown: 0, status: 'proposed', run_id: 'r1',
  provider_config_id: 'pc1', accepted_rule_id: null, decided_by: '', decided_at: null,
  created_at: '2026-10-29T00:00:00Z', updated_at: '2026-10-29T00:00:00Z',
}

const run: RuleProposalRun = {
  id: 'r1', workflow_id: 'w1', label: 'review', trigger: 'manual', actor: 'josh', status: 'succeeded',
  provider_config_id: 'pc1', evidence_count: 4, proposal_count: 1,
  flag_ideas: [{ idea: 'Flag lockfile changes', rationale: 'deps broke twice' }], error: '',
  cost_usd: 0.25, input_tokens: 10, output_tokens: 5, started_at: '2026-10-29T00:00:00Z', finished_at: '2026-10-29T00:01:00Z',
}

function renderIt(onRuleAdded = vi.fn()) {
  render(<MemoryRouter><GateRuleProposals workflowId="w1" label="review" onRuleAdded={onRuleAdded} /></MemoryRouter>)
  return onRuleAdded
}

describe('GateRuleProposals', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.providers.mockResolvedValue([{ id: 'pc1', name: 'Claude', provider: 'claude', model: '', env: {} }])
    m.list.mockResolvedValue([])
    m.runs.mockResolvedValue([])
  })

  it('asks for a model when proposals are off and saves the choice', async () => {
    const user = userEvent.setup()
    m.getSettings.mockResolvedValue(baseSettings)
    m.updateSettings.mockResolvedValue({ ...baseSettings, proposal_provider_config_id: 'pc1' })
    renderIt()
    await user.click(await screen.findByRole('button', { name: 'Use this model' }))
    expect(m.updateSettings).toHaveBeenCalledWith({ ...baseSettings, proposal_provider_config_id: 'pc1' })
    expect(await screen.findByRole('button', { name: 'Find rules' })).toBeInTheDocument()
  })

  it('shows proposals with their replay counts and accepts one', async () => {
    const user = userEvent.setup()
    m.getSettings.mockResolvedValue({ ...baseSettings, proposal_provider_config_id: 'pc1' })
    m.list.mockResolvedValueOnce([proposal]).mockResolvedValue([])
    m.runs.mockResolvedValue([run])
    m.accept.mockResolvedValue({ proposal: { ...proposal, status: 'accepted' }, rule: {} })
    const onRuleAdded = renderIt()
    expect(await screen.findByText(/three migrations broke/)).toBeInTheDocument()
    expect(screen.getByText(/Would have caught/).textContent).toMatch(/3 of 4 .* 1 of 20/)
    expect(screen.getByRole('link', { name: 'Add index' })).toHaveAttribute('href', '/tasks/t1')
    expect(screen.getByText(/Flag lockfile changes/)).toBeInTheDocument()
    expect(screen.getByRole('status').textContent).toMatch(/1 suggestion from 4 .*\$0\.25/)
    await user.click(screen.getByRole('button', { name: /Add rule files match/ }))
    expect(m.accept).toHaveBeenCalledWith('p1')
    await waitFor(() => expect(onRuleAdded).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByText(/three migrations broke/)).not.toBeInTheDocument())
  })

  it('starts a run and shows a failure message from the last run', async () => {
    const user = userEvent.setup()
    m.getSettings.mockResolvedValue({ ...baseSettings, proposal_provider_config_id: 'pc1' })
    m.runs.mockResolvedValue([{ ...run, status: 'failed', error: 'model: rate limited' }])
    m.run.mockResolvedValue({ ...run, status: 'running', finished_at: null })
    renderIt()
    expect(await screen.findByText(/failed .*rate limited/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Find rules' }))
    expect(m.run).toHaveBeenCalledWith('w1', 'review')
    expect(await screen.findByRole('button', { name: 'Looking…' })).toBeDisabled()
  })
})
