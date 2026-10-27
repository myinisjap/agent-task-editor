import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import RunHistoryList from './RunHistoryList'
import type { AgentRun, AgentConfig, ProviderConfig } from '../../api/client'

const agent = {
  id: 'a1', name: 'Coder', effort: 'low', provider_config_id: 'pc1',
  escalation_ladder: [{ provider_config_id: 'pc2', effort: 'max' }],
} as unknown as AgentConfig
const pcs = [
  { id: 'pc1', name: 'cheap', provider: 'claude', model: 'sonnet' },
  { id: 'pc2', name: 'big', provider: 'claude', model: 'opus' },
] as unknown as ProviderConfig[]

function renderRuns(runs: Partial<AgentRun>[]) {
  render(
    <RunHistoryList
      runs={runs as AgentRun[]}
      agentConfigs={[agent]}
      providerConfigs={pcs}
      selectedRun={null}
      onSelectRun={vi.fn()}
      isRunning={false}
      canRerun={false}
      onStop={vi.fn()}
      onRerun={vi.fn()}
      actionPending={false}
    />,
  )
}

describe('RunHistoryList ladder badge', () => {
  it('shows rung, model and effort', () => {
    renderRuns([{ id: 'run-0000001', agent_config_id: 'a1', provider_config_id: 'pc2', status: 'completed', ladder_rung: 1 }])
    expect(screen.getByText('rung 1: opus / max')).toBeInTheDocument()
  })
  it('omits the badge when ladder_rung is null', () => {
    renderRuns([{ id: 'run-0000002', agent_config_id: 'a1', status: 'completed', ladder_rung: null }])
    expect(screen.queryByText(/^rung /)).not.toBeInTheDocument()
  })
})
