import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import NeedsYouPanel from './NeedsYouPanel'
import { buildInbox } from '../../lib/inbox'
import type { NeedsMeItem, PolicyApprovalItem, Workflow } from '../../api/client'

const wf = {
  id: 'w', name: 'W', labels: [{ name: 'review', display_name: 'Review' }, { name: 'done', display_name: 'Done' }],
  transitions: [{ from_label: 'review', to_label: 'done', trigger_type: 'human', path: 'success' }],
} as unknown as Workflow

const pending = (over: Partial<NeedsMeItem> = {}): NeedsMeItem => ({
  task_id: 'p1', task_title: 'Pending one', repo_id: 'r', workflow_id: 'w', label: 'review', reason: 'auto_approving',
  since: '2026-01-01T00:00:00Z', due_at: new Date(Date.now() + 90_000).toISOString(), decision_id: 'd1',
  risk_flags: [], verification: 'pass', changed: { files: 2, additions: 10, deletions: 3 }, ...over,
} as NeedsMeItem)
const approval = (over: Partial<PolicyApprovalItem> = {}): PolicyApprovalItem => ({
  task_id: 'a1', task_title: 'Approved one', workflow_id: 'w', label: 'done', gate_label: 'review',
  approved_at: new Date(Date.now() - 3600_000).toISOString(), note: 'n', decision_id: 'd2', undoable: true, ...over,
} as PolicyApprovalItem)
const human = { task_id: 'h1', task_title: 'Human one', repo_id: 'r', workflow_id: 'w', label: 'review', reason: 'human_gate', since: '2026-01-01T00:00:00Z' } as NeedsMeItem

afterEach(() => vi.useRealTimers())

describe('NeedsYouPanel autonomy groups', () => {
  it('renders the pending row with target, chips, diff, countdown and Hold; count ignores it', () => {
    const sections = buildInbox([human], [], [], [], { autoApproving: [pending()] })
    const onHold = vi.fn()
    render(<NeedsYouPanel sections={sections} onSelect={vi.fn()} workflows={[wf]} onHold={onHold} />)
    const row = screen.getByTestId('auto-approving-row')
    expect(row).toHaveTextContent('Pending one')
    expect(row).toHaveTextContent('Review → Done')
    expect(row).toHaveTextContent('no flags')
    expect(row).toHaveTextContent('verify passed')
    expect(row).toHaveTextContent('+10')
    expect(screen.getByTestId('autonomy-countdown')).toHaveTextContent(/approves in 1:[23]\d/)
    expect(screen.getByTestId('needs-you-panel')).toHaveTextContent(/1 (item|thing)|\b1\b/)
    expect(screen.queryByText('Review all 2')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Hold Pending one' }))
    expect(onHold).toHaveBeenCalledTimes(1)
  })

  it('h on a focused pending row holds', () => {
    const sections = buildInbox([], [], [], [], { autoApproving: [pending()] })
    const onHold = vi.fn()
    render(<NeedsYouPanel sections={sections} onSelect={vi.fn()} workflows={[wf]} onHold={onHold} />)
    fireEvent.keyDown(document.querySelector('[data-entry-key="p1"]') as HTMLElement, { key: 'h' })
    expect(onHold).toHaveBeenCalledTimes(1)
  })

  it('counts down live', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const sections = buildInbox([], [], [], [], { autoApproving: [pending({ due_at: '2026-01-01T00:01:00Z' })] })
    render(<NeedsYouPanel sections={sections} onSelect={vi.fn()} workflows={[wf]} />)
    expect(screen.getByTestId('autonomy-countdown')).toHaveTextContent('1:00')
    act(() => { vi.advanceTimersByTime(5000) })
    expect(screen.getByTestId('autonomy-countdown')).toHaveTextContent('0:5')
  })

  it('approved header shows 0 collapsed, and N expanded', async () => {
    const { rerender } = render(<NeedsYouPanel sections={buildInbox([], [], [])} onSelect={vi.fn()} workflows={[wf]} />)
    const header = screen.getByRole('button', { name: /Approved for you today · 0/ })
    expect(header).toHaveAttribute('aria-expanded', 'false')
    rerender(<NeedsYouPanel sections={buildInbox([], [], [], [], { approved: [approval()] })} onSelect={vi.fn()} workflows={[wf]} />)
    expect(screen.getByRole('button', { name: /Approved for you today · 1/ })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('policy-approval-row')).toHaveTextContent('Approved one')
    await userEvent.click(screen.getByRole('button', { name: /Approved for you today · 1/ }))
    expect(screen.queryByTestId('policy-approval-row')).toBeNull()
  })

  it('undo shows an inline confirm naming the gate, with the PR note', async () => {
    const onUndo = vi.fn()
    const sections = buildInbox([], [], [], [], { approved: [approval({ pr_url: 'https://example.test/pr/1' })] })
    render(<NeedsYouPanel sections={sections} onSelect={vi.fn()} workflows={[wf]} onUndo={onUndo} />)
    await userEvent.click(screen.getByRole('button', { name: 'Undo Approved one' }))
    const confirm = screen.getByTestId('undo-confirm')
    expect(confirm).toHaveTextContent('Approved one')
    expect(confirm).toHaveTextContent(/pull request was already opened/i)
    await userEvent.click(screen.getByRole('button', { name: 'Undo approval' }))
    expect(onUndo).toHaveBeenCalledTimes(1)
  })

  it('disables Undo when not undoable', () => {
    const sections = buildInbox([], [], [], [], { approved: [approval({ undoable: false })] })
    render(<NeedsYouPanel sections={sections} onSelect={vi.fn()} workflows={[wf]} />)
    expect(screen.getByRole('button', { name: 'Undo Approved one' })).toBeDisabled()
  })
})
