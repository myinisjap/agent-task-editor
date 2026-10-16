import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import InboxPage from './InboxPage'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { useWorkflowStore } from '../stores/workflow'

const m = {
  needsMe: vi.fn(), apply: vi.fn(), discard: vi.fn(), list: vi.fn(), approve: vi.fn(), rerun: vi.fn(), retry: vi.fn(), dismiss: vi.fn(), health: vi.fn(), resume: vi.fn(),
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
        rerun: (...a: unknown[]) => m.rerun(...a),
        retrySideEffects: (...a: unknown[]) => m.retry(...a),
        dismissSideEffects: (...a: unknown[]) => m.dismiss(...a),
        applySourceUpdate: (...a: unknown[]) => m.apply(...a),
        discardSourceUpdate: (...a: unknown[]) => m.discard(...a),
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
    window.matchMedia = ((q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {} })) as never
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
