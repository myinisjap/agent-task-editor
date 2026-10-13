import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import SideEffectBadge from './SideEffectBadge'

const base = { next_at: '2026-01-01T00:00:00Z', last_error: null }

describe('SideEffectBadge', () => {
  it('renders nothing when there are no effects', () => {
    const { container } = render(<SideEffectBadge effects={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows "failed, retrying" for a pending effect with failed attempts and surfaces the error', () => {
    render(<SideEffectBadge effects={[{ ...base, kind: 'create_pr', status: 'pending', attempts: 2, last_error: 'gh: auth' }]} />)
    const el = screen.getByRole('status')
    expect(el).toHaveTextContent('PR creation failed, retrying')
    expect(el).toHaveAttribute('title', 'PR creation failed, retrying: gh: auth')
  })

  it('prefers a failed effect and offers Retry', () => {
    const onRetry = vi.fn()
    render(
      <SideEffectBadge
        onRetry={onRetry}
        effects={[
          { ...base, kind: 'leave_not_ready', status: 'pending', attempts: 1 },
          { ...base, kind: 'create_pr', status: 'failed', attempts: 8 },
        ]}
      />,
    )
    expect(screen.getByRole('status')).toHaveTextContent('PR creation failed')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
  })

  it('shows pending without a retry button for a fresh effect', () => {
    render(<SideEffectBadge onRetry={() => {}} effects={[{ ...base, kind: 'terminal', status: 'pending', attempts: 0 }]} />)
    expect(screen.getByRole('status')).toHaveTextContent('Branch push pending')
    expect(screen.queryByRole('button')).toBeNull()
  })
})
