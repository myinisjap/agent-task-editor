import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useDebouncedCallback } from './useDebouncedCallback'

describe('useDebouncedCallback', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('only invokes the wrapped function once after the trailing delay, for a burst of calls', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 250))

    result.current()
    result.current()
    result.current()

    expect(fn).not.toHaveBeenCalled()

    vi.advanceTimersByTime(250)

    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('calls the latest closure passed on the most recent render, not a stale one', () => {
    let value = 'first'
    const fn = vi.fn(() => value)
    const { result, rerender } = renderHook(() => useDebouncedCallback(fn, 100))

    result.current()
    value = 'second'
    rerender()

    vi.advanceTimersByTime(100)

    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveReturnedWith('second')
  })

  it('returns a stable function identity across renders', () => {
    const fn = vi.fn()
    const { result, rerender } = renderHook(() => useDebouncedCallback(fn, 100))
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })

  it('clears the pending timer on unmount so it never fires', () => {
    const fn = vi.fn()
    const { result, unmount } = renderHook(() => useDebouncedCallback(fn, 100))

    result.current()
    unmount()

    vi.advanceTimersByTime(200)

    expect(fn).not.toHaveBeenCalled()
  })
})
