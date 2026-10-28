import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom'
import { useState } from 'react'
import ReviewPopup from './ReviewPopup'
import { useNeedsMeStore } from '../../stores/needsMe'
import { useInboxStore, useInboxSections } from '../../stores/inbox'
import { useHealthStore } from '../../stores/health'
import { useToastStore } from '../../stores/toast'
import { flattenInbox } from '../../lib/inbox'

const m = { hold: vi.fn(), undo: vi.fn(), needsMe: vi.fn(), approve: vi.fn(), reject: vi.fn(), list: vi.fn(), health: vi.fn(), reply: vi.fn() }
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      needsMe: { get: (...a: unknown[]) => m.needsMe(...a).then((r: unknown) => (Array.isArray(r) ? { items: r, approved_by_policy: [] } : r)) },
      autonomy: { hold: (...a: unknown[]) => m.hold(...a), undo: (...a: unknown[]) => m.undo(...a) },
      tasks: {
        list: (...a: unknown[]) => m.list(...a),
        approve: (...a: unknown[]) => m.approve(...a),
        reject: (...a: unknown[]) => m.reject(...a),
        replyRun: (...a: unknown[]) => m.reply(...a),
        reviewBrief: () => Promise.resolve({ human_gate: false }),
        previews: () => Promise.resolve({ capture: null }),
        autonomyDecision: () => Promise.resolve(null),
      },
      health: { providers: (...a: unknown[]) => m.health(...a) },
    },
  }
})

const gate = (id: string, title: string) => ({
  task_id: id, task_title: title, repo_id: 'r', workflow_id: 'w', label: 'review', reason: 'human_gate',
  since: new Date(Date.now() - 3 * 3600_000).toISOString(),
})
const workflow = {
  id: 'w', name: 'W', labels: [],
  transitions: [
    { from_label: 'review', to_label: 'done', trigger_type: 'human', path: 'success' },
    { from_label: 'review', to_label: 'dev', trigger_type: 'human', path: 'failure' },
  ],
}
const answer = {
  ...gate('t3', 'Asked'), reason: 'waiting_human', escalation_reason: 'agent_question', label: 'dev', run_id: 'run1', message: 'Which database?',
}

function Harness({ initial, onClose }: { initial: string; onClose?: () => void }) {
  const sections = useInboxSections()
  const flat = flattenInbox(sections)
  const [key, setKey] = useState<string | undefined>(initial)
  const loc = useLocation()
  return (
    <>
      <div data-testid="loc">{loc.pathname}</div>
      <ReviewPopup flat={flat} selectedKey={key} onMove={setKey} onClose={onClose ?? (() => undefined)} workflows={[workflow] as never} />
    </>
  )
}
const renderPopup = (initial = 't1', onClose?: () => void) =>
  render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<Harness initial={initial} onClose={onClose} />} />
        <Route path="/tasks/:id" element={<div data-testid="detail" />} />
      </Routes>
    </MemoryRouter>,
  )
const load = async () => {
  await useNeedsMeStore.getState().refresh()
}

beforeEach(() => {
  Object.values(m).forEach((f) => f.mockReset())
  m.needsMe.mockResolvedValue([gate('t1', 'Ship it'), gate('t2', 'Second')])
  m.list.mockResolvedValue({ items: [], nextCursor: null })
  m.health.mockResolvedValue({ checks: [], paused_providers: [] })
  useNeedsMeStore.setState({ items: [], byTask: {}, loaded: false })
  useInboxStore.setState({ failedEffects: [], sourceEdits: [], loaded: false })
  useHealthStore.setState({ checks: [], paused: [], loaded: false })
})
afterEach(() => vi.useRealTimers())

const title = () => screen.getByTestId('inbox-preview').querySelector('h2')?.textContent

describe('ReviewPopup', () => {
  it('is a modal dialog with "i of n" header and traps Tab', async () => {
    await load()
    renderPopup()
    const dialog = screen.getByRole('dialog', { name: 'Needs you review' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(screen.getByRole('heading', { name: 'Needs you · 1 of 2' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Previous item' })).toBeDisabled()
    // Tab from the last focusable wraps to the first.
    const focusable = dialog.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), textarea, [tabindex]:not([tabindex="-1"])')
    focusable[focusable.length - 1].focus()
    await userEvent.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
    expect(document.activeElement).toBe(focusable[0])
  })

  it('moves with j/k and arrows, clamped, and with the buttons', async () => {
    await load()
    renderPopup()
    await userEvent.keyboard('k')
    expect(title()).toBe('Ship it')
    await userEvent.keyboard('j')
    await waitFor(() => expect(title()).toBe('Second'))
    expect(screen.getByRole('heading', { name: 'Needs you · 2 of 2' })).toBeInTheDocument()
    await userEvent.keyboard('{ArrowDown}')
    expect(title()).toBe('Second')
    await userEvent.keyboard('{ArrowUp}')
    await waitFor(() => expect(title()).toBe('Ship it'))
    await userEvent.click(screen.getByRole('button', { name: 'Next item' }))
    await waitFor(() => expect(title()).toBe('Second'))
  })

  it('approves with a and advances to the next item', async () => {
    await load()
    m.approve.mockResolvedValue({})
    renderPopup()
    m.needsMe.mockResolvedValue([gate('t2', 'Second')])
    await userEvent.keyboard('a')
    expect(m.approve).toHaveBeenCalledWith('t1')
    await waitFor(() => expect(title()).toBe('Second'))
  })

  it('r opens and focuses the note; Esc closes only the note, then the popup', async () => {
    await load()
    const onClose = vi.fn()
    renderPopup('t1', onClose)
    await userEvent.keyboard('r')
    const ta = await screen.findByLabelText('Request changes note')
    await waitFor(() => expect(ta).toHaveFocus())
    await userEvent.keyboard('j')
    expect(ta).toHaveValue('j')
    expect(title()).toBe('Ship it')
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByLabelText('Request changes note')).toBeNull()
    expect(onClose).not.toHaveBeenCalled()
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('request changes sends the note', async () => {
    await load()
    m.reject.mockResolvedValue({})
    renderPopup()
    await userEvent.click(screen.getByRole('button', { name: 'Request changes' }))
    const send = screen.getByRole('button', { name: 'Send' })
    expect(send).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Request changes note'), '  tighten it ')
    await userEvent.click(send)
    expect(m.reject).toHaveBeenCalledWith('t1', 'tighten it')
  })

  it('r on an answer item focuses the reply box, and reply sends', async () => {
    m.needsMe.mockResolvedValue([answer])
    m.reply.mockResolvedValue({})
    await load()
    renderPopup('t3')
    await userEvent.keyboard('r')
    const box = screen.getByLabelText('Reply to agent')
    expect(box).toHaveFocus()
    await userEvent.type(box, 'Postgres')
    await userEvent.click(screen.getByRole('button', { name: 'Send' }))
    expect(m.reply).toHaveBeenCalledWith('t3', 'run1', 'Postgres')
  })

  it('shows an action error inline', async () => {
    await load()
    m.approve.mockRejectedValue(new Error('nope'))
    renderPopup()
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('nope')
  })

  it('Enter opens the full task page', async () => {
    await load()
    renderPopup()
    await userEvent.keyboard('{Enter}')
    expect(await screen.findByTestId('detail')).toBeInTheDocument()
  })

  it('shows All clear after the last item and closes after ~1.5s', async () => {
    m.needsMe.mockResolvedValue([gate('t1', 'Only')])
    await load()
    m.approve.mockResolvedValue({})
    const onClose = vi.fn()
    renderPopup('t1', onClose)
    m.needsMe.mockResolvedValue([])
    vi.useFakeTimers({ shouldAdvanceTime: true })
    await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole('button', { name: 'Approve' }))
    expect(await screen.findByTestId('review-all-clear')).toHaveTextContent('All clear')
    expect(onClose).not.toHaveBeenCalled()
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  describe('autonomy entries', () => {
    const pending = {
      ...gate('p1', 'Pending task'), reason: 'auto_approving', decision_id: 'd1', verification: 'pass', risk_flags: [],
      due_at: new Date(Date.now() + 120_000).toISOString(),
    }
    const approval = {
      task_id: 'a1', task_title: 'Approved task', workflow_id: 'w', label: 'done', gate_label: 'review',
      approved_at: new Date(Date.now() - 600_000).toISOString(), note: 'Auto-approved: no flags', decision_id: 'd2', undoable: true,
    }

    it('shows the pending body and Hold moves it to Decide', async () => {
      m.needsMe.mockResolvedValue([pending])
      await load()
      m.hold.mockResolvedValue({})
      renderPopup('p1')
      expect(screen.getByTestId('autonomy-countdown')).toHaveTextContent(/approves in/)
      m.needsMe.mockResolvedValue([gate('p1', 'Pending task')])
      await userEvent.click(screen.getByRole('button', { name: 'Hold' }))
      expect(m.hold).toHaveBeenCalledWith('p1')
      expect(await screen.findByRole('button', { name: 'Approve' })).toBeInTheDocument()
      expect(title()).toBe('Pending task')
      expect(useToastStore.getState().toast?.message).toMatch(/Held "Pending task"/)
      expect(useToastStore.getState().toast?.action?.label).toBe('Undo')
    })

    it('h holds the selected pending entry', async () => {
      m.needsMe.mockResolvedValue([pending])
      await load()
      m.hold.mockResolvedValue({})
      renderPopup('p1')
      await userEvent.keyboard('h')
      expect(m.hold).toHaveBeenCalledWith('p1')
    })

    it('Review now holds too', async () => {
      m.needsMe.mockResolvedValue([pending])
      await load()
      m.hold.mockResolvedValue({})
      renderPopup('p1')
      await userEvent.click(screen.getByRole('button', { name: 'Review now' }))
      expect(m.hold).toHaveBeenCalledWith('p1')
    })

    it('approved body shows the note; Undo confirms then calls the API', async () => {
      m.needsMe.mockResolvedValue({ items: [], approved_by_policy: [approval] })
      await load()
      m.undo.mockResolvedValue({ message: 'Moved back', pr_left_open: false })
      renderPopup('a1:approved')
      expect(screen.getByText('Auto-approved: no flags')).toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
      expect(m.undo).not.toHaveBeenCalled()
      await userEvent.click(screen.getByRole('button', { name: 'Undo approval' }))
      expect(m.undo).toHaveBeenCalledWith('a1')
    })
  })
})
