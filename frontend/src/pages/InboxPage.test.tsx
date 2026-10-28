import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { useLocation } from 'react-router-dom'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import InboxPage from './InboxPage'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { useWorkflowStore } from '../stores/workflow'

const m = {
  needsMe: vi.fn(), reject: vi.fn(), apply: vi.fn(), discard: vi.fn(), list: vi.fn(), approve: vi.fn(), rerun: vi.fn(), retry: vi.fn(), dismiss: vi.fn(), health: vi.fn(), resume: vi.fn(), reply: vi.fn(),
}
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      needsMe: { list: (...a: unknown[]) => m.needsMe(...a) },
      tasks: {
        list: (...a: unknown[]) => m.list(...a),
        approve: (...a: unknown[]) => m.approve(...a),
        reject: (...a: unknown[]) => m.reject(...a),
        rerun: (...a: unknown[]) => m.rerun(...a),
        retrySideEffects: (...a: unknown[]) => m.retry(...a),
        dismissSideEffects: (...a: unknown[]) => m.dismiss(...a),
        applySourceUpdate: (...a: unknown[]) => m.apply(...a),
        discardSourceUpdate: (...a: unknown[]) => m.discard(...a),
        replyRun: (...a: unknown[]) => m.reply(...a),
        reviewBrief: () => Promise.resolve({ human_gate: false }),
        previews: () => Promise.resolve({ capture: null }),
      },
      health: { providers: (...a: unknown[]) => m.health(...a), resumeProvider: (...a: unknown[]) => m.resume(...a) },
    },
  }
})

const gateItem = {
  task_id: 't1', task_title: 'Ship it', repo_id: 'r', workflow_id: 'w', label: 'review', reason: 'human_gate',
  since: new Date(Date.now() - 3 * 3600_000).toISOString(),
}
const workflow = {
  id: 'w', name: 'W', labels: [],
  transitions: [{ from_label: 'review', to_label: 'done', trigger_type: 'human', path: 'success' }],
}

const renderPage = () => render(<MemoryRouter><InboxPage /></MemoryRouter>)

beforeEach(() => {
  Object.values(m).forEach((f) => f.mockReset())
  m.needsMe.mockResolvedValue([])
  m.list.mockResolvedValue({ items: [], nextCursor: null })
  m.health.mockResolvedValue({ checks: [], paused_providers: [] })
  useNeedsMeStore.setState({ items: [], byTask: {} })
  useInboxStore.setState({ failedEffects: [], sourceEdits: [], loaded: false })
  useHealthStore.setState({ checks: [], paused: [], loaded: false })
  useWorkflowStore.setState({ workflows: [workflow] as never })
})

describe('InboxPage', () => {
  it('shows the empty state when nothing waits', async () => {
    renderPage()
    expect(await screen.findByTestId('inbox-empty')).toBeInTheDocument()
  })

  it('lists approvals with age and approves in one click', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    m.approve.mockResolvedValue({})
    renderPage()
    expect(await screen.findByRole('link', { name: 'Ship it' })).toHaveAttribute('href', '/tasks/t1')
    expect(screen.getByText('3h ago')).toBeInTheDocument()
    m.needsMe.mockResolvedValue([])
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(m.approve).toHaveBeenCalledWith('t1')
    await waitFor(() => expect(screen.getByTestId('inbox-empty')).toBeInTheDocument())
  })

  it('shows risk flag chips on human-gate rows that have them', async () => {
    m.needsMe.mockResolvedValue([
      { ...gateItem, risk_flags: ['migration', 'tests_removed'] },
      { ...gateItem, task_id: 't2', task_title: 'Plain' },
    ])
    renderPage()
    await screen.findByText('Ship it')
    expect(screen.getAllByTestId('risk-flag-chips')).toHaveLength(1)
    expect(screen.getByTestId('risk-flag-migration')).toHaveTextContent('Migration')
    expect(screen.getByTestId('risk-flag-tests_removed')).toHaveTextContent('Tests removed')
  })

  it('shows decision context on a Decide row', async () => {
    m.needsMe.mockResolvedValue([
      { ...gateItem, summary: 'Adds the migration', changed: { files: 3, additions: 12, deletions: 4 }, cost_usd: 1.5, verification: 'fail', preview_count: 2 },
    ])
    renderPage()
    await screen.findByText('Ship it')
    expect(screen.getByTestId('inbox-section-decide')).toBeInTheDocument()
    expect(screen.getByText('Adds the migration')).toBeInTheDocument()
    expect(screen.getByTestId('decision-diff')).toHaveTextContent('+12 −4 · 3 files')
    expect(screen.getByText('$1.50')).toBeInTheDocument()
    expect(screen.getByText('Verify failed')).toBeInTheDocument()
    expect(screen.getByText(/2 previews/)).toBeInTheDocument()
  })

  it('request changes needs a note and rejects with it', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    m.reject.mockResolvedValue({})
    useWorkflowStore.setState({
      workflows: [{ ...workflow, transitions: [...workflow.transitions, { from_label: 'review', to_label: 'dev', trigger_type: 'human', path: 'failure' }] }] as never,
    })
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: 'Request changes' }))
    const send = screen.getByRole('button', { name: 'Send' })
    expect(send).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Request changes note'), '  tighten it ')
    m.needsMe.mockResolvedValue([])
    await userEvent.click(send)
    expect(m.reject).toHaveBeenCalledWith('t1', 'tighten it')
  })

  it('puts a login failure under Fix setup, not Answer', async () => {
    m.needsMe.mockResolvedValue([{ ...gateItem, task_id: 't9', task_title: 'Stuck', reason: 'waiting_human', escalation_reason: 'auth_required', label: 'dev' }])
    renderPage()
    await screen.findByText('Stuck')
    expect(screen.getByTestId('inbox-section-fix')).toHaveTextContent('Stuck')
    expect(screen.queryByTestId('inbox-section-answer')).toBeNull()
    expect(screen.getByRole('link', { name: 'Open Health' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('heading count includes a paused provider', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    m.health.mockResolvedValue({ checks: [], paused_providers: [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }] })
    renderPage()
    await screen.findByText(/Claude dispatch is paused/)
    expect(screen.getByTestId('inbox-total')).toHaveTextContent('(2)')
  })

  it('retries a failed side effect', async () => {
    m.list.mockResolvedValue({
      items: [{ id: 't2', title: 'PR task', side_effects: [{ kind: 'create_pr', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z', last_error: 'boom' }] }],
      nextCursor: null,
    })
    m.retry.mockResolvedValue({})
    renderPage()
    expect(await screen.findByText(/PR creation failed: boom/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(m.retry).toHaveBeenCalledWith('t2')
  })

  it('dismisses a failed side effect', async () => {
    m.list.mockResolvedValue({
      items: [{ id: 't2', title: 'PR task', side_effects: [{ kind: 'create_pr', status: 'failed', attempts: 5, next_at: '2026-01-01T00:00:00Z', last_error: 'boom' }] }],
      nextCursor: null,
    })
    m.dismiss.mockResolvedValue(undefined)
    renderPage()
    await screen.findByText(/PR creation failed: boom/)
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(m.dismiss).toHaveBeenCalledWith('t2')
  })

  it('lists a source issue edit held after approval and applies or discards it', async () => {
    m.list.mockResolvedValue({
      items: [{ id: 't3', title: 'Edited', source_pending_update: { title: 'x', description: 'y', type: 'task', created_at: '2026-01-01T00:00:00Z' } }],
      nextCursor: null,
    })
    m.apply.mockResolvedValue({})
    m.discard.mockResolvedValue({})
    renderPage()
    expect(await screen.findByText(/edited after approval/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Edited' })).toHaveAttribute('href', '/tasks/t3')
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(m.apply).toHaveBeenCalledWith('t3')
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(m.discard).toHaveBeenCalledWith('t3')
  })

  it('resumes a paused provider', async () => {
    m.health.mockResolvedValue({ checks: [], paused_providers: [{ provider: 'claude', reason: 'Not logged in', paused_at: '2026-01-01T00:00:00Z' }] })
    m.resume.mockResolvedValue(undefined)
    renderPage()
    await screen.findByText(/Claude dispatch is paused/)
    await userEvent.click(screen.getByRole('button', { name: 'Resume' }))
    expect(m.resume).toHaveBeenCalledWith('claude')
  })

  it('shows an action error inline', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    m.approve.mockRejectedValue(new Error('nope'))
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: 'Approve' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('nope')
  })
})

describe('InboxPage phone layout', () => {
  it('taps through the whole row on phones', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    const orig = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: q.includes('max-width: 767px'), media: q, addEventListener() {}, removeEventListener() {} })) as never
    try {
      render(
        <MemoryRouter>
          <Routes>
            <Route path="/" element={<InboxPage />} />
            <Route path="/tasks/:id" element={<div>detail page</div>} />
          </Routes>
        </MemoryRouter>,
      )
      await userEvent.click(await screen.findByTestId('inbox-item'))
      expect(await screen.findByText('detail page')).toBeInTheDocument()
    } finally {
      window.matchMedia = orig
    }
  })

  it('gives action buttons a 44px phone tap target', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    renderPage()
    expect((await screen.findByRole('button', { name: 'Approve' })).className).toContain('min-h-[44px]')
  })
})

const gate2 = { ...gateItem, task_id: 't2', task_title: 'Second', since: new Date(Date.now() - 2 * 3600_000).toISOString() }
const answerItem = {
  ...gateItem, task_id: 't3', task_title: 'Asked', reason: 'waiting_human', escalation_reason: 'agent_question', label: 'dev',
  run_id: 'run1', message: 'Which database?', since: new Date(Date.now() - 1 * 3600_000).toISOString(),
}
const fixItem = {
  ...gateItem, task_id: 't4', task_title: 'Broken', reason: 'waiting_human', escalation_reason: 'auth_required', label: 'dev',
  since: new Date(Date.now() - 30 * 60_000).toISOString(),
}

function Loc() {
  return <div data-testid="loc">{useLocation().search}</div>
}
const renderWide = (entry = '/') =>
  render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/" element={<><InboxPage /><Loc /></>} />
        <Route path="/tasks/:id" element={<div data-testid="detail" />} />
      </Routes>
    </MemoryRouter>,
  )

describe('InboxPage desktop two-pane', () => {
  const orig = window.matchMedia
  beforeEach(() => {
    window.matchMedia = ((q: string) => ({ matches: q.includes('min-width: 1280px'), media: q, addEventListener() {}, removeEventListener() {} })) as never
  })
  afterEach(() => {
    window.matchMedia = orig
  })
  const selected = () => screen.getByTestId('inbox-preview').querySelector('h2')?.textContent

  it('selects the first item by default and writes it to the URL', async () => {
    m.needsMe.mockResolvedValue([gateItem, gate2])
    renderWide()
    await waitFor(() => expect(screen.getByTestId('inbox-preview')).toHaveTextContent('Ship it'))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('?task=t1'))
  })

  it('restores the selection from ?task=', async () => {
    m.needsMe.mockResolvedValue([gateItem, gate2])
    renderWide('/?task=t2')
    await waitFor(() => expect(selected()).toBe('Second'))
    expect(screen.getByTestId('loc')).toHaveTextContent('?task=t2')
  })

  it('moves with j/k and arrows, clamped at both ends', async () => {
    m.needsMe.mockResolvedValue([gateItem, gate2])
    renderWide()
    await waitFor(() => expect(selected()).toBe('Ship it'))
    await userEvent.keyboard('k')
    expect(selected()).toBe('Ship it')
    await userEvent.keyboard('j')
    await waitFor(() => expect(selected()).toBe('Second'))
    await userEvent.keyboard('{ArrowDown}')
    expect(selected()).toBe('Second')
    await userEvent.keyboard('{ArrowUp}')
    await waitFor(() => expect(selected()).toBe('Ship it'))
  })

  it('approves with a and advances to the next item', async () => {
    m.needsMe.mockResolvedValue([gateItem, gate2])
    m.approve.mockResolvedValue({})
    renderWide()
    await waitFor(() => expect(selected()).toBe('Ship it'))
    m.needsMe.mockResolvedValue([gate2])
    await userEvent.keyboard('a')
    expect(m.approve).toHaveBeenCalledWith('t1')
    await waitFor(() => expect(selected()).toBe('Second'))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('?task=t2'))
  })

  it('r opens and focuses the note; typing does not navigate; Esc closes', async () => {
    m.needsMe.mockResolvedValue([gateItem, gate2])
    useWorkflowStore.setState({
      workflows: [{ ...workflow, transitions: [...workflow.transitions, { from_label: 'review', to_label: 'dev', trigger_type: 'human', path: 'failure' }] }] as never,
    })
    renderWide()
    await waitFor(() => expect(selected()).toBe('Ship it'))
    await userEvent.keyboard('r')
    const ta = await screen.findByLabelText('Request changes note')
    await waitFor(() => expect(ta).toHaveFocus())
    await userEvent.keyboard('j')
    expect(ta).toHaveValue('j')
    expect(selected()).toBe('Ship it')
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByLabelText('Request changes note')).toBeNull()
  })

  it('Enter opens the full task page', async () => {
    m.needsMe.mockResolvedValue([gateItem])
    renderWide()
    await waitFor(() => expect(selected()).toBe('Ship it'))
    await userEvent.keyboard('{Enter}')
    expect(await screen.findByTestId('detail')).toBeInTheDocument()
  })

  it('shows the question and a reply box for an Answer item', async () => {
    m.needsMe.mockResolvedValue([answerItem])
    m.reply.mockResolvedValue({})
    renderWide()
    const preview = await screen.findByTestId('inbox-preview')
    await waitFor(() => expect(preview).toHaveTextContent('Which database?'))
    await userEvent.type(screen.getByLabelText('Reply to agent'), 'Postgres')
    await userEvent.click(screen.getByRole('button', { name: 'Send' }))
    expect(m.reply).toHaveBeenCalledWith('t3', 'run1', 'Postgres')
  })

  it('shows the explanation and fix link for a Fix setup item', async () => {
    m.needsMe.mockResolvedValue([fixItem])
    renderWide()
    const preview = await screen.findByTestId('inbox-preview')
    await waitFor(() => expect(preview).toHaveTextContent('Broken'))
    expect(preview.querySelector('a[href="/settings/health"]')).not.toBeNull()
    expect(preview).toHaveTextContent(/log/i)
  })

  it('is off below 1280px: no preview and j does nothing', async () => {
    window.matchMedia = orig
    m.needsMe.mockResolvedValue([gateItem, gate2])
    renderWide()
    await screen.findByText('Second')
    expect(screen.queryByTestId('inbox-preview')).toBeNull()
    await userEvent.keyboard('j')
    expect(screen.getByTestId('loc')).toHaveTextContent('')
    expect(screen.getByTestId('loc').textContent).toBe('')
  })
})
