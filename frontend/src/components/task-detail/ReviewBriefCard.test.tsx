import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import ReviewBriefCard from './ReviewBriefCard'
import { api, type ReviewBrief } from '../../api/client'

vi.mock('../../api/client', async (orig) => {
  const mod = await orig<typeof import('../../api/client')>()
  return { ...mod, api: { ...mod.api, tasks: { ...mod.api.tasks, reviewBrief: vi.fn() } } }
})

const base: ReviewBrief = {
  human_gate: true,
  changed: { files: 2, additions: 10, deletions: 3, top_files: [{ path: 'a/b.go', status: 'modified', additions: 10, deletions: 3 }] },
  risk_flags: [{ code: 'migration', reason: '1 migration/schema file(s) changed', files: ['db/migrations/1.sql'] }],
  verification: { status: 'pass', command: 'make test', exit_code: 0, duration_ms: 1500, output_tail: 'ok', truncated: false, created_at: '2026-01-01T00:00:00Z' },
  runs: { total: 3, failed: 1, rework_loops: 2, cost_usd: 1.234, cost_unknown: true },
  agent_summary: { source: 'stored_info', run_id: 'r1', agent_name: 'Reviewer', text: 'Changed: stuff', truncated: false },
  diff_available: true,
  truncated: false,
  computed_at: '2026-01-01T00:00:00Z',
}

describe('ReviewBriefCard', () => {
  beforeEach(() => vi.mocked(api.tasks.reviewBrief).mockReset())

  it('renders all sections', async () => {
    vi.mocked(api.tasks.reviewBrief).mockResolvedValue(base)
    render(<ReviewBriefCard taskId="t1" />)
    await waitFor(() => expect(screen.getByTestId('review-brief')).toBeInTheDocument())
    expect(screen.getByText(/2 files, \+10 −3/)).toBeInTheDocument()
    expect(screen.getByTestId('risk-flag-migration')).toBeInTheDocument()
    expect(screen.getByTestId('brief-verification-badge')).toHaveTextContent('Verification passed')
    expect(screen.getByText(/runs 3 \(1 failed\) · rework loops 2 · \$1\.23 \+ unknown/)).toBeInTheDocument()
    expect(screen.getByText('Changed: stuff')).toBeInTheDocument()
  })

  it('hides the preview strip when hidePreview is set', async () => {
    vi.mocked(api.tasks.reviewBrief).mockResolvedValue(base)
    const spy = vi.spyOn(api.tasks, 'previews').mockResolvedValue({ capture: null } as never)
    render(<ReviewBriefCard taskId="t1" hidePreview />)
    await waitFor(() => expect(screen.getByTestId('review-brief')).toBeInTheDocument())
    expect(spy).not.toHaveBeenCalled()
  })

  it('shows empty states', async () => {
    vi.mocked(api.tasks.reviewBrief).mockResolvedValue({
      ...base, risk_flags: [], verification: null, agent_summary: null, diff_available: false,
      changed: { files: 0, additions: 0, deletions: 0, top_files: [] }, runs: { ...base.runs, cost_unknown: false },
    })
    render(<ReviewBriefCard taskId="t1" />)
    await waitFor(() => expect(screen.getByText('No risk flags')).toBeInTheDocument())
    expect(screen.getByText('No branch diff yet')).toBeInTheDocument()
    expect(screen.getByText('No verify command ran')).toBeInTheDocument()
  })

  it('renders nothing on error or when not a human gate', async () => {
    vi.mocked(api.tasks.reviewBrief).mockRejectedValue(new Error('x'))
    const { container, rerender } = render(<ReviewBriefCard taskId="t1" />)
    await waitFor(() => expect(api.tasks.reviewBrief).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
    vi.mocked(api.tasks.reviewBrief).mockResolvedValue({ ...base, human_gate: false })
    rerender(<ReviewBriefCard taskId="t2" />)
    await waitFor(() => expect(api.tasks.reviewBrief).toHaveBeenCalledTimes(2))
    expect(container).toBeEmptyDOMElement()
  })
})
