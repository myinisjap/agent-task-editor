// issue #350 — useDashboard used to refetch GET /dashboard once per
// task.label_changed/agent_started/agent_done/needs_human WS event with no
// debounce, so a burst of simultaneous events (several agents finishing
// together) fanned out into one request per event. Assert it's now
// coalesced behind a trailing debounce.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useDashboard } from './useDashboard'

const dashboardGetMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      dashboard: { get: (...args: unknown[]) => dashboardGetMock(...args) },
    },
  }
})

let wsOnHandler: ((event: { type: string; payload: unknown }) => void) | null = null

vi.mock('../api/ws', () => ({
  wsClient: {
    on: vi.fn((h: (event: { type: string; payload: unknown }) => void) => {
      wsOnHandler = h
      return () => {}
    }),
  },
}))

describe('useDashboard WS refresh debounce (#350)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    dashboardGetMock.mockReset().mockResolvedValue({})
    wsOnHandler = null
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('coalesces a burst of task-level WS events into a single re-fetch', async () => {
    renderHook(() => useDashboard())

    // Mount's own immediate refresh().
    await vi.waitFor(() => expect(dashboardGetMock).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(wsOnHandler).not.toBeNull())

    wsOnHandler!({ type: 'task.label_changed', payload: {} })
    wsOnHandler!({ type: 'task.agent_started', payload: {} })
    wsOnHandler!({ type: 'task.agent_done', payload: {} })
    wsOnHandler!({ type: 'task.needs_human', payload: {} })

    // Still just the mount fetch — the debounce hasn't elapsed yet.
    expect(dashboardGetMock).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(250)

    expect(dashboardGetMock).toHaveBeenCalledTimes(2)
  })
})
