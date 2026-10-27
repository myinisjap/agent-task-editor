import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AgentHistoryPanel from './AgentHistoryPanel'

const versionsMock = vi.fn()
const restoreMock = vi.fn()

vi.mock('../../api/client', () => ({
  api: {
    agents: {
      versions: (...a: unknown[]) => versionsMock(...a),
      restoreVersion: (...a: unknown[]) => restoreMock(...a),
    },
  },
}))

const snap = (prompt: string, effort = '') => ({ system_prompt: prompt, effort, max_turns: 50 })
const versions = [
  { id: 'b', agent_config_id: 'a1', version: 2, snapshot: snap('line1\nnew', 'high'), actor_id: 'alice', note: 'tweak', created_at: '2026-01-02T00:00:00Z' },
  { id: 'a', agent_config_id: 'a1', version: 1, snapshot: snap('line1\nold'), actor_id: null, note: null, created_at: '2026-01-01T00:00:00Z' },
]

beforeEach(() => {
  versionsMock.mockReset().mockResolvedValue(versions)
  restoreMock.mockReset().mockResolvedValue({ id: 'a1' })
})

describe('AgentHistoryPanel', () => {
  it('lists versions and shows a prompt diff', async () => {
    const user = userEvent.setup()
    render(<AgentHistoryPanel agentId="a1" onRestored={vi.fn()} />)
    await screen.findByText('v2')
    expect(screen.getByText('current')).toBeInTheDocument()
    expect(screen.getByText('tweak')).toBeInTheDocument()
    await user.click(screen.getByText('v2'))
    expect(screen.getByText('- old')).toBeInTheDocument()
    expect(screen.getByText('+ new')).toBeInTheDocument()
    expect(screen.getByText(/effort: \(empty\) → high/)).toBeInTheDocument()
  })

  it('restores via an in-page confirm, not window.confirm', async () => {
    const user = userEvent.setup()
    const confirmSpy = vi.spyOn(window, 'confirm')
    const onRestored = vi.fn()
    render(<AgentHistoryPanel agentId="a1" onRestored={onRestored} />)
    await screen.findByText('v1')
    await user.click(screen.getByRole('button', { name: 'Restore' }))
    expect(screen.getByText(/Restore v1 as a new version\?/)).toBeInTheDocument()
    expect(restoreMock).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(restoreMock).toHaveBeenCalledWith('a1', 1))
    expect(onRestored).toHaveBeenCalled()
    expect(confirmSpy).not.toHaveBeenCalled()
  })
})
