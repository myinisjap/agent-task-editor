import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { wsClient } from '../../api/ws'
import userEvent from '@testing-library/user-event'
import VerificationPanel from './VerificationPanel'
import type { RunVerification } from '../../api/client'

const m = { verifications: vi.fn() }
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, api: { tasks: { verifications: (...a: unknown[]) => m.verifications(...a) } } }
})

const v = (over: Partial<RunVerification> = {}): RunVerification => ({
  run_id: 'r1', task_id: 't1', command: 'go test ./...', status: 'pass', exit_code: 0,
  output: 'ok  all packages', truncated: false, duration_ms: 2500, head_sha: 'abc', reused_from_run_id: null, created_at: '2026-01-01T00:00:00Z', ...over,
})

beforeEach(() => m.verifications.mockReset())

describe('VerificationPanel', () => {
  it('renders nothing when there is no verification', async () => {
    m.verifications.mockResolvedValue([])
    const { container } = render(<VerificationPanel taskId="t1" />)
    await waitFor(() => expect(m.verifications).toHaveBeenCalledWith('t1'))
    expect(container).toBeEmptyDOMElement()
  })

  it('shows a pass badge, the command and duration', async () => {
    m.verifications.mockResolvedValue([v()])
    render(<VerificationPanel taskId="t1" />)
    expect(await screen.findByTestId('verification-badge')).toHaveTextContent('Verification passed')
    expect(screen.getByText('go test ./...')).toBeInTheDocument()
    expect(screen.getByText(/exit 0 · 2\.5s/)).toBeInTheDocument()
  })

  it('shows a fail badge with collapsible output, using only the latest result', async () => {
    m.verifications.mockResolvedValue([
      v({ status: 'fail', exit_code: 1, output: 'FAIL TestX', truncated: true }),
      v({ run_id: 'r0', status: 'pass' }),
    ])
    render(<VerificationPanel taskId="t1" />)
    expect(await screen.findByTestId('verification-badge')).toHaveTextContent('Verification failed')
    const details = screen.getByText('Output').closest('details') as HTMLDetailsElement
    expect(details.open).toBe(false)
    await userEvent.click(screen.getByText('Output'))
    expect(details.open).toBe(true)
    expect(screen.getByText('FAIL TestX')).toBeInTheDocument()
    expect(screen.getByText(/Earlier output truncated/)).toBeInTheDocument()
  })

  it('labels timeouts and refetches when refreshKey changes', async () => {
    m.verifications.mockResolvedValue([v({ status: 'timeout', exit_code: null })])
    const { rerender } = render(<VerificationPanel taskId="t1" refreshKey={0} />)
    expect(await screen.findByTestId('verification-badge')).toHaveTextContent('timed out')
    rerender(<VerificationPanel taskId="t1" refreshKey={1} />)
    await waitFor(() => expect(m.verifications).toHaveBeenCalledTimes(2))
  })

  it('shows reuse instead of a duration for a reused pass', async () => {
    m.verifications.mockResolvedValue([v({ duration_ms: 0, reused_from_run_id: 'run-origin-1' })])
    render(<VerificationPanel taskId="t1" />)
    expect(await screen.findByText(/reused from run run-orig/)).toBeInTheDocument()
  })

  it('shows a Verifying row between the running event and the final status', async () => {
    m.verifications.mockResolvedValue([])
    const handlers: Array<(e: unknown) => void> = []
    const spy = vi.spyOn(wsClient, 'on').mockImplementation((h) => {
      handlers.push(h as (e: unknown) => void)
      return () => {}
    })
    render(<VerificationPanel taskId="t1" />)
    act(() => handlers.forEach((h) => h({ type: 'task.verification', payload: { task_id: 't1', run_id: 'r', status: 'running' } })))
    expect(await screen.findByTestId('verification-running')).toBeInTheDocument()
    act(() => handlers.forEach((h) => h({ type: 'task.verification', payload: { task_id: 't1', run_id: 'r', status: 'pass' } })))
    expect(screen.queryByTestId('verification-running')).not.toBeInTheDocument()
    spy.mockRestore()
  })
})
