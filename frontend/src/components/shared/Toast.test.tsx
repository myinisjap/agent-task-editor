import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import Toast from './Toast'

afterEach(() => vi.useRealTimers())

describe('Toast', () => {
  it('renders the message as a status', () => {
    render(<Toast message="Saved" onDismiss={() => {}} />)
    expect(screen.getByRole('status')).toHaveTextContent('Saved')
  })

  it('auto-dismisses after the timeout', () => {
    vi.useFakeTimers()
    const onDismiss = vi.fn()
    render(<Toast message="Saved" onDismiss={onDismiss} timeoutMs={1000} />)
    act(() => { vi.advanceTimersByTime(999) })
    expect(onDismiss).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(2) })
    expect(onDismiss).toHaveBeenCalledTimes(1)
  })

  it('dismisses via the × button', () => {
    const onDismiss = vi.fn()
    render(<Toast message="Saved" onDismiss={onDismiss} />)
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(onDismiss).toHaveBeenCalled()
  })
})
