import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import type { AgentRun } from '../../api/client'
import TaskActions from './TaskActions'

function renderActions(overrides: Partial<React.ComponentProps<typeof TaskActions>> = {}) {
  const props: React.ComponentProps<typeof TaskActions> = {
    activeRun: undefined,
    needsHuman: false,
    openCommentsCount: 0,
    replyText: '',
    setReplyText: vi.fn(),
    onReply: vi.fn(),
    rejectNote: '',
    setRejectNote: vi.fn(),
    onReject: vi.fn(),
    onApprove: vi.fn(),
    canApprove: true,
    canReject: true,
    actionPending: false,
    onJumpToDiffTab: vi.fn(),
    ...overrides,
  }
  render(
    <MemoryRouter>
      <TaskActions {...props} />
    </MemoryRouter>,
  )
  return props
}

describe('TaskActions enablement rules', () => {
  it('enables Approve unless actionPending', () => {
    renderActions()
    expect(screen.getByRole('button', { name: 'Approve' })).toBeEnabled()

    renderActionsCleanup()
    renderActions({ actionPending: true })
    expect(screen.getAllByRole('button', { name: 'Approve' }).at(-1)).toBeDisabled()
  })

  it('disables Reject when rejectNote is empty and there are no open comments', () => {
    renderActions({ rejectNote: '', openCommentsCount: 0 })
    expect(screen.getByRole('button', { name: 'Reject' })).toBeDisabled()
  })

  it('enables Reject when rejectNote has text', () => {
    renderActions({ rejectNote: 'please fix x', openCommentsCount: 0 })
    expect(screen.getByRole('button', { name: 'Reject' })).toBeEnabled()
  })

  it('enables Reject when there are open comments even with an empty note', () => {
    renderActions({ rejectNote: '', openCommentsCount: 2 })
    expect(screen.getByRole('button', { name: 'Reject' })).toBeEnabled()
  })

  it('does not render the reply box when needsHuman is false', () => {
    renderActions({ needsHuman: false })
    expect(screen.queryByRole('button', { name: 'Reply & Continue' })).not.toBeInTheDocument()
  })

  it('renders the reply box when needsHuman is true, disabled until replyText is non-blank', () => {
    renderActions({ needsHuman: true, replyText: '' })
    expect(screen.getByRole('button', { name: 'Reply & Continue' })).toBeDisabled()

    renderActionsCleanup()
    renderActions({ needsHuman: true, replyText: 'here is my answer' })
    expect(screen.getAllByRole('button', { name: 'Reply & Continue' }).at(-1)).toBeEnabled()
  })

  it('calls onApprove/onReject/onReply when their buttons are clicked', async () => {
    const user = userEvent.setup()
    const props = renderActions({
      needsHuman: true,
      replyText: 'answer',
      rejectNote: 'nope',
    })

    await user.click(screen.getByRole('button', { name: 'Approve' }))
    expect(props.onApprove).toHaveBeenCalledTimes(1)

    await user.click(screen.getByRole('button', { name: 'Reject' }))
    expect(props.onReject).toHaveBeenCalledTimes(1)

    await user.click(screen.getByRole('button', { name: 'Reply & Continue' }))
    expect(props.onReply).toHaveBeenCalledTimes(1)
  })
})

// Testing Library's `render` mounts a new tree each call rather than
// replacing the last one, so back-to-back `renderActions()` calls in the
// same test would otherwise leave both trees mounted and produce ambiguous
// queries. This mirrors `cleanup()` from @testing-library/react (also run
// automatically in afterEach via src/test/setup.ts) for use mid-test.
function renderActionsCleanup() {
  document.body.innerHTML = ''
}

describe('TaskActions human-transition gating', () => {
  it('hides Approve and Reject when no human transition exists, leaving Reply', () => {
    renderActions({ needsHuman: true, replyText: 'x', canApprove: false, canReject: false })
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reject' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reply & Continue' })).toBeEnabled()
  })

  it('shows only Approve when only a success transition exists', () => {
    renderActions({ canApprove: true, canReject: false })
    expect(screen.getByRole('button', { name: 'Approve' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reject' })).not.toBeInTheDocument()
  })

  it('shows only Reject when only a failure transition exists', () => {
    renderActions({ canApprove: false, canReject: true, rejectNote: 'n' })
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reject' })).toBeInTheDocument()
  })

  it('renders an action error inline without calling window.alert', () => {
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    renderActions({ needsHuman: true, actionError: 'boom' })
    expect(screen.getByRole('alert')).toHaveTextContent('boom')
    expect(alertSpy).not.toHaveBeenCalled()
    alertSpy.mockRestore()
  })
})

function waitingRun(over: Partial<AgentRun>): AgentRun {
  return { id: 'r1', task_id: 't1', status: 'waiting_human', created_at: '2026-01-01T00:00:00Z', ...over } as AgentRun
}

describe('TaskActions escalation reason', () => {
  it('shows the reason heading and stored notes for a failure escalation', () => {
    renderActions({
      needsHuman: true,
      onRetry: vi.fn(),
      activeRun: waitingRun({ escalation_reason: 'auth_required', notes: 'Agent failed: not logged in.' }),
    })
    expect(screen.getByText('Agent CLI is not logged in')).toBeInTheDocument()
    expect(screen.getByText('Agent failed: not logged in.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Health' })).toHaveAttribute('href', '/settings/health')
    // Reply is demoted but still available.
    expect(screen.getByRole('button', { name: 'Reply & Continue' })).toBeInTheDocument()
  })

  it('offers Retry as the primary action for a retryable failure', async () => {
    const user = userEvent.setup()
    const props = renderActions({
      needsHuman: true,
      onRetry: vi.fn(),
      activeRun: waitingRun({ escalation_reason: 'transient_exhausted', notes: 'gave up' }),
    })
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(props.onRetry).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('keeps Reply primary, with no Retry, for an agent question or an unrecorded reason', () => {
    renderActions({ needsHuman: true, onRetry: vi.fn(), activeRun: waitingRun({ escalation_reason: 'agent_question', feedback: 'which db?' }) })
    expect(screen.getByText('Agent asked a question')).toBeInTheDocument()
    expect(screen.getByText('which db?')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Retry/ })).not.toBeInTheDocument()

    renderActionsCleanup()
    renderActions({ needsHuman: true, onRetry: vi.fn(), activeRun: waitingRun({ escalation_reason: null }) })
    expect(screen.getByText('Agent is waiting for your input')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Retry/ })).not.toBeInTheDocument()
  })
})

describe('TaskActions phone bar', () => {
  it('is a sticky bottom bar offset by the bottom-bar CSS variable', () => {
    renderActions()
    const cls = screen.getByTestId('task-actions-bar').className
    expect(cls).toContain('max-md:sticky')
    expect(cls).toContain('--ate-bottom-bar-height')
    expect(screen.getByRole('button', { name: 'Approve' }).className).toContain('min-h-[44px]')
  })
})

import { rejectDisabled, rejectPlaceholder } from './TaskActions'
describe('rejectDisabled', () => {
  it('needs a note or open comments, and not pending', () => {
    expect(rejectDisabled('', 0, false)).toBe(true)
    expect(rejectDisabled('  ', 0, false)).toBe(true)
    expect(rejectDisabled('x', 0, false)).toBe(false)
    expect(rejectDisabled('', 2, false)).toBe(false)
    expect(rejectDisabled('x', 0, true)).toBe(true)
    expect(rejectPlaceholder(0)).toMatch(/required/)
    expect(rejectPlaceholder(1)).toMatch(/optional/)
  })
})
