import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AgentExperimentPanel from './AgentExperimentPanel'

const listMock = vi.fn()
const getMock = vi.fn()
const versionsMock = vi.fn()
const startMock = vi.fn()
const stopMock = vi.fn()
const promoteMock = vi.fn()

vi.mock('../../api/client', () => ({
  api: {
    agents: {
      experiments: (...a: unknown[]) => listMock(...a),
      experiment: (...a: unknown[]) => getMock(...a),
      versions: (...a: unknown[]) => versionsMock(...a),
      startExperiment: (...a: unknown[]) => startMock(...a),
      stopExperiment: (...a: unknown[]) => stopMock(...a),
      promoteExperiment: (...a: unknown[]) => promoteMock(...a),
    },
  },
}))

const arm = (over = {}) => ({
  tasks: 5,
  tasks_done: 3,
  first_pass_success_rate_percent: 66,
  rework_rate_percent: 0,
  human_touch_rate_percent: 33,
  avg_cost_to_done_usd: 1.5,
  avg_duration_secs: 42,
  not_enough_data: true,
  ...over,
})
const running = {
  id: 'e1',
  agent_config_id: 'a1',
  candidate_snapshot: {},
  share_percent: 30,
  status: 'running',
  note: null,
  started_at: '2026-01-01T00:00:00Z',
  results: { control: arm(), candidate: arm({ tasks: 2 }), min_tasks_per_arm: 10 },
}
const config = { id: 'a1', current_version: 1 } as any

beforeEach(() => {
  for (const m of [listMock, getMock, versionsMock, startMock, stopMock, promoteMock]) m.mockReset()
  listMock.mockResolvedValue([])
  getMock.mockResolvedValue(running)
  versionsMock.mockResolvedValue([{ version: 1, snapshot: { system_prompt: 'old', provider_config_id: 'p1', effort: '', max_turns: 50 } }])
  startMock.mockResolvedValue(running)
  stopMock.mockResolvedValue({ ...running, status: 'stopped' })
  promoteMock.mockResolvedValue({ experiment: { ...running, status: 'promoted' }, config: { id: 'a1' } })
})

describe('AgentExperimentPanel', () => {
  it('offers to start when none is running and submits an edited copy', async () => {
    const user = userEvent.setup()
    render(<AgentExperimentPanel agentId="a1" config={config} providerConfigs={[{ id: 'p1', name: 'Prov' } as any]} onPromoted={vi.fn()} />)
    await user.click(await screen.findByRole('button', { name: 'Start experiment' }))
    const prompt = await screen.findByLabelText('Candidate system prompt')
    await user.clear(prompt)
    await user.type(prompt, 'new')
    await user.click(screen.getByRole('button', { name: 'Start' }))
    await waitFor(() => expect(startMock).toHaveBeenCalled())
    const [id, body] = startMock.mock.calls[0]
    expect(id).toBe('a1')
    expect(body.share_percent).toBe(50)
    expect(body.candidate.system_prompt).toBe('new')
    expect(body.candidate.max_turns).toBe(50)
  })

  it('shows per-arm results with a not-enough-data badge', async () => {
    listMock.mockResolvedValue([running])
    render(<AgentExperimentPanel agentId="a1" config={config} onPromoted={vi.fn()} />)
    await screen.findByLabelText('Experiment results')
    expect(screen.getAllByText(/Not enough data/)).toHaveLength(2)
  })

  it('promotes via in-page confirm and reports the config', async () => {
    const user = userEvent.setup()
    const onPromoted = vi.fn()
    listMock.mockResolvedValue([running])
    render(<AgentExperimentPanel agentId="a1" config={config} onPromoted={onPromoted} />)
    await user.click(await screen.findByRole('button', { name: 'Promote' }))
    await user.type(screen.getByLabelText('Promote note'), 'ship')
    await user.click(screen.getByRole('button', { name: 'Confirm promote' }))
    await waitFor(() => expect(promoteMock).toHaveBeenCalledWith('a1', 'e1', 'ship'))
    expect(onPromoted).toHaveBeenCalledWith({ id: 'a1' })
  })

  it('stops after confirm', async () => {
    const user = userEvent.setup()
    listMock.mockResolvedValue([running])
    render(<AgentExperimentPanel agentId="a1" config={config} onPromoted={vi.fn()} />)
    await user.click(await screen.findByRole('button', { name: 'Stop' }))
    await user.click(screen.getByRole('button', { name: 'Confirm stop' }))
    await waitFor(() => expect(stopMock).toHaveBeenCalledWith('a1', 'e1'))
  })
})
