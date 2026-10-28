import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const handlers: ((e: unknown) => void)[] = []
vi.mock('../api/ws', () => ({
  wsClient: {
    on: (h: (e: unknown) => void) => {
      handlers.push(h)
      return () => handlers.splice(handlers.indexOf(h), 1)
    },
  },
}))

import { shouldFlash, useNeedsYouFlash } from './useNeedsYouFlash'

describe('shouldFlash', () => {
  it('only on increase over a known baseline', () => {
    expect(shouldFlash(undefined, 5)).toBe(false)
    expect(shouldFlash(0, 1)).toBe(true)
    expect(shouldFlash(2, 2)).toBe(false)
    expect(shouldFlash(3, 2)).toBe(false)
  })
})

describe('useNeedsYouFlash', () => {
  beforeEach(() => { vi.useFakeTimers(); handlers.length = 0 })
  afterEach(() => vi.useRealTimers())

  it('does not flash on load, flashes on increase, clears after 3600ms', () => {
    const { result, rerender } = renderHook(({ c, l }) => useNeedsYouFlash(c, l), { initialProps: { c: 0, l: false } })
    rerender({ c: 4, l: true })
    expect(result.current.flashing).toBe(false)
    rerender({ c: 3, l: true })
    expect(result.current.flashing).toBe(false)
    rerender({ c: 3, l: true })
    expect(result.current.flashing).toBe(false)
    rerender({ c: 4, l: true })
    expect(result.current.flashing).toBe(true)
    act(() => { vi.advanceTimersByTime(3599) })
    expect(result.current.flashing).toBe(true)
    act(() => { vi.advanceTimersByTime(2) })
    expect(result.current.flashing).toBe(false)
  })

  it('flashes on WS task.needs_human', () => {
    const { result } = renderHook(() => useNeedsYouFlash(0, true))
    act(() => handlers.forEach((h) => h({ type: 'task.needs_human', payload: {} })))
    expect(result.current.flashing).toBe(true)
  })
})
