import { act, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AutonomyDecisionBanner from './AutonomyDecisionBanner'

vi.mock('../../api/client', () => ({ api: { tasks: { autonomyDecision: vi.fn() } } }))
import { api } from '../../api/client'

const mock = api.tasks.autonomyDecision as unknown as ReturnType<typeof vi.fn>
const dec = (mode: string, decision: string, reasons: { code: string; detail: string }[]) => ({
  id: '1', task_id: 't', workflow_id: 'w', label: 'review', mode, decision, reasons, created_at: '',
})
const labels = [{ name: 'review', display_name: 'Review' }] as never
const base = { taskId: 't', refreshKey: 'k', labels, alwaysAsk: false }

describe('AutonomyDecisionBanner', () => {
  beforeEach(() => { mock.mockReset() })

  it('approve state', async () => {
    mock.mockResolvedValue(dec('on_flags', 'approve', [{ code: 'no_blocking_flags', detail: '' }]))
    render(<AutonomyDecisionBanner {...base} verification="pass" />)
    expect(await screen.findByText('This would auto-approve')).toBeInTheDocument()
    expect(screen.getByText('Review gate set to Ask on flags · no flags · verify passed')).toBeInTheDocument()
  })

  it('ask state', async () => {
    mock.mockResolvedValue(dec('on_flags', 'ask', [
      { code: 'flag_raised', detail: 'migration' },
      { code: 'verify_failed', detail: 'verification status: fail' },
    ]))
    render(<AutonomyDecisionBanner {...base} />)
    expect(await screen.findByText('The gate would still ask you')).toBeInTheDocument()
    expect(screen.getByTestId('risk-flag-migration')).toBeInTheDocument()
    expect(screen.getByText('Verification failed')).toBeInTheDocument()
  })

  it('renders nothing for mode ask', async () => {
    mock.mockResolvedValue(dec('ask', 'ask', [{ code: 'mode_ask', detail: '' }]))
    render(<AutonomyDecisionBanner {...base} />)
    await waitFor(() => expect(mock).toHaveBeenCalled())
    expect(screen.queryByTestId('autonomy-banner')).toBeNull()
  })

  it('renders nothing on 404', async () => {
    mock.mockRejectedValue(new Error('404'))
    render(<AutonomyDecisionBanner {...base} />)
    await act(async () => { await new Promise((r) => setTimeout(r, 10)) })
    expect(screen.queryByTestId('autonomy-banner')).toBeNull()
  })

  it('alwaysAsk overrides approve', async () => {
    mock.mockResolvedValue(dec('auto', 'approve', [{ code: 'auto_policy', detail: '' }]))
    render(<AutonomyDecisionBanner {...base} alwaysAsk />)
    expect(await screen.findByText('The gate would still ask you')).toBeInTheDocument()
    expect(screen.getByText('You marked this task Always ask')).toBeInTheDocument()
  })
})
