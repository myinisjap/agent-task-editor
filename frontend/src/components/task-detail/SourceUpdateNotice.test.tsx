import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SourceUpdateNotice from './SourceUpdateNotice'
import type { Task } from '../../api/client'

const m = { apply: vi.fn(), discard: vi.fn() }
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      tasks: {
        applySourceUpdate: (...a: unknown[]) => m.apply(...a),
        discardSourceUpdate: (...a: unknown[]) => m.discard(...a),
      },
    },
  }
})

const task = (over: Record<string, unknown> = {}) =>
  ({
    id: 't1', title: 'Old title', description: 'old body', type: 'task',
    source_pending_update: { title: 'New title', description: 'new body', type: 'bug', created_at: '2026-01-01T00:00:00Z' },
    ...over,
  }) as unknown as Task

beforeEach(() => {
  m.apply.mockReset()
  m.discard.mockReset()
})

describe('SourceUpdateNotice', () => {
  it('renders nothing without a pending update', () => {
    const { container } = render(<SourceUpdateNotice task={task({ source_pending_update: null })} onChanged={vi.fn()} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the warning with current and new text', () => {
    render(<SourceUpdateNotice task={task()} onChanged={vi.fn()} />)
    expect(screen.getByText('The source issue was edited after approval')).toBeInTheDocument()
    expect(screen.getByText('old body')).toBeInTheDocument()
    expect(screen.getByText('new body')).toBeInTheDocument()
    expect(screen.getByText('New title', { selector: 'pre' })).toBeInTheDocument()
  })

  it('applies and reports the refreshed task', async () => {
    const updated = task({ source_pending_update: null, title: 'New title' })
    m.apply.mockResolvedValue(updated)
    const onChanged = vi.fn()
    render(<SourceUpdateNotice task={task()} onChanged={onChanged} />)
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(onChanged).toHaveBeenCalledWith(updated))
    expect(m.apply).toHaveBeenCalledWith('t1')
    expect(m.discard).not.toHaveBeenCalled()
  })

  it('discards', async () => {
    const updated = task({ source_pending_update: null })
    m.discard.mockResolvedValue(updated)
    const onChanged = vi.fn()
    render(<SourceUpdateNotice task={task()} onChanged={onChanged} />)
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(onChanged).toHaveBeenCalledWith(updated))
    expect(m.discard).toHaveBeenCalledWith('t1')
  })

  it('shows an error and keeps the notice when the call fails', async () => {
    m.apply.mockRejectedValue(new Error('boom'))
    const onChanged = vi.fn()
    render(<SourceUpdateNotice task={task()} onChanged={onChanged} />)
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(await screen.findByText('boom')).toBeInTheDocument()
    expect(onChanged).not.toHaveBeenCalled()
  })
})
