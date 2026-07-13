import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
    actionPending: false,
    onJumpToDiffTab: vi.fn(),
    ...overrides,
  }
  render(<TaskActions {...props} />)
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
