import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import ShouldHaveAskedDialog from './ShouldHaveAskedDialog'
import { useToastStore } from '../../stores/toast'

const m = { suggestions: vi.fn(), sha: vi.fn(), backtest: vi.fn() }
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: { autonomy: {
      suggestions: (...a: unknown[]) => m.suggestions(...a),
      shouldHaveAsked: (...a: unknown[]) => m.sha(...a),
      backtest: (...a: unknown[]) => m.backtest(...a),
    } },
  }
})

const sugg = {
  task_id: 't1', workflow_id: 'w', repo_id: 'r1', label: 'review', policy_approved: true, decision_id: 'd', diff_available: true,
  changed_files: 6, changed_lines: 200,
  path_globs: [{ glob: 'backend/internal/remotemcp/**', kind: 'dir', files: 4 }, { glob: 'docs/**', kind: 'dir', files: 1 }],
  task_type: 'feature', min_changed_lines: 150,
}
const workflow = {
  id: 'w', name: 'W',
  labels: [
    { name: 'review', display_name: 'Review', autonomy: { mode: 'auto' }, is_terminal: 0 },
    { name: 'qa', display_name: 'QA', autonomy: { mode: 'on_flags' }, is_terminal: 0 },
    { name: 'ship', display_name: 'Ship', autonomy: { mode: 'ask' }, is_terminal: 0 },
  ],
} as never
const bt = (o = {}) => ({ rules: { policy_approvals: 10, evaluated: 10, unknown: 0, would_stop: 3, stopped: [{ task_id: 't1' }], ...o } })

async function renderDialog(props: Partial<React.ComponentProps<typeof ShouldHaveAskedDialog>> = {}) {
  const onClose = vi.fn(), onDone = vi.fn()
  render(<ShouldHaveAskedDialog taskId="t1" taskTitle="Do thing" done={false} workflow={workflow} repoName="myrepo" onClose={onClose} onDone={onDone} {...props} />)
  await act(async () => {})
  return { onClose, onDone }
}
const tick = (name: RegExp | string) => fireEvent.click(screen.getByLabelText(name))
const settle = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(450) }) }

beforeEach(() => {
  vi.useFakeTimers()
  useToastStore.setState({ toast: null })
  m.suggestions.mockResolvedValue(sugg)
  m.backtest.mockResolvedValue(bt())
  m.sha.mockResolvedValue({ escape: {}, rule: { summary: 'under x' }, lesson: null, sent_back: false, step_down: null, task: {} })
})
afterEach(() => { vi.useRealTimers(); Object.values(m).forEach((f) => f.mockReset()) })

describe('ShouldHaveAskedDialog', () => {
  it('is a labelled dialog with suggestion checkboxes and focus inside', async () => {
    await renderDialog()
    const dlg = screen.getByRole('dialog', { name: 'Should have asked' })
    expect(dlg.contains(document.activeElement)).toBe(true)
    expect(screen.getByLabelText(/Changes under backend\/internal\/remotemcp\/, 4 of 6 files/)).toBeInTheDocument()
    expect(screen.getByLabelText('Any feature task')).toBeInTheDocument()
    expect(screen.getByLabelText('More than 150 lines changed')).toBeInTheDocument()
    expect(screen.getByLabelText('Nothing specific, just log it')).toBeChecked()
    expect(screen.getByRole('button', { name: 'Log it' })).toBeInTheDocument()
  })

  it('"just log it" is exclusive and the button label follows', async () => {
    await renderDialog()
    tick('Any feature task')
    expect(screen.getByLabelText('Nothing specific, just log it')).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Add rule' })).toBeInTheDocument()
    tick('Nothing specific, just log it')
    expect(screen.getByLabelText('Any feature task')).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Log it' })).toBeInTheDocument()
  })

  it('combines ticks into one rule and posts scope', async () => {
    const { onClose, onDone } = await renderDialog()
    tick(/Changes under backend/); tick('Any feature task'); tick('More than 150 lines changed')
    expect(screen.getByText('This repo (myrepo)')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('All gates'))
    fireEvent.click(screen.getByLabelText('All repos'))
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Add rule' })) })
    expect(m.sha).toHaveBeenCalledWith('t1', {
      rule: { path_globs: ['backend/internal/remotemcp/**'], task_type: 'feature', min_changed_lines: 150 },
      scope_repo: false, label_scope: 'all_gates', send_back_note: undefined, lesson: undefined,
    })
    expect(useToastStore.getState().toast?.message).toBe('Added ask rule: under x.')
    expect(onDone).toHaveBeenCalled(); expect(onClose).toHaveBeenCalled()
  })

  it('runs one debounced backtest excluding this task', async () => {
    await renderDialog()
    tick('Any feature task'); tick('More than 150 lines changed')
    expect(screen.getByText('Checking the last 30 days…')).toBeInTheDocument()
    expect(m.backtest).not.toHaveBeenCalled()
    await settle()
    expect(m.backtest).toHaveBeenCalledTimes(1)
    expect(m.backtest).toHaveBeenCalledWith('w', 'review', { mode: 'ask', days: 30, extra_rules: [{ task_type: 'feature', min_changed_lines: 150, repo_id: 'r1' }] })
    expect(screen.getByTestId('sha-backtest')).toHaveTextContent('This rule would also have stopped 2 of the 9 tasks auto-approved in the last 30 days')
  })

  it('all gates backtests each autonomy gate and drops repo_id for all repos', async () => {
    await renderDialog()
    tick('Any feature task')
    fireEvent.click(screen.getByLabelText('All gates')); fireEvent.click(screen.getByLabelText('All repos'))
    await settle()
    expect(m.backtest.mock.calls.map((c) => c[1]).sort()).toEqual(['qa', 'review'])
    expect(m.backtest.mock.calls[0][2].extra_rules[0]).toEqual({ task_type: 'feature' })
  })

  it('backtest failure is non-blocking', async () => {
    m.backtest.mockRejectedValue(new Error('x'))
    await renderDialog()
    tick('Any feature task'); await settle()
    expect(screen.getByText("Couldn't check recent approvals.")).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add rule' })).toBeEnabled()
  })

  it('hides send back when done; requires a note otherwise', async () => {
    await renderDialog({ done: true })
    expect(screen.queryByText('Send back to work with a note')).toBeNull()
  })

  it('send back needs a note and posts it', async () => {
    await renderDialog()
    fireEvent.click(screen.getByLabelText('Send back to work with a note'))
    expect(screen.getByRole('button', { name: 'Log it' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Note for the agent'), { target: { value: ' fix it ' } })
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Log it' })) })
    expect(m.sha.mock.calls[0][1]).toMatchObject({ send_back_note: 'fix it', rule: undefined })
  })

  it('lesson only with path globs, prefilled, capped and sent', async () => {
    await renderDialog()
    expect(screen.queryByLabelText(/save a lesson/)).toBeNull()
    tick('Any feature task')
    expect(screen.queryByLabelText(/save a lesson/)).toBeNull()
    tick(/Changes under backend/)
    tick(/save a lesson/)
    const ta = screen.getByLabelText('Lesson') as HTMLTextAreaElement
    expect(ta.value).toContain('backend/internal/remotemcp/**')
    expect(ta.value).toContain('Do thing')
    expect(ta).toHaveAttribute('maxlength', '300')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Add rule' })) })
    expect(m.sha.mock.calls[0][1].lesson).toEqual({ body: ta.value, kind: 'trap' })
  })

  it('shows the error and stays open on failure', async () => {
    m.sha.mockRejectedValue(new Error('nope'))
    const { onClose } = await renderDialog()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Log it' })) })
    expect(screen.getByRole('alert')).toHaveTextContent('nope')
    expect(onClose).not.toHaveBeenCalled()
  })

  it('offers only Cancel when the policy did not approve', async () => {
    m.suggestions.mockResolvedValue({ ...sugg, policy_approved: false })
    await renderDialog()
    expect(screen.getByText(/policy didn't approve/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Log it' })).toBeNull()
  })

  it('shows a load error', async () => {
    m.suggestions.mockRejectedValue(new Error('bad'))
    await renderDialog()
    expect(screen.getByRole('alert')).toHaveTextContent('bad')
  })
})
