import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

let handler: ((e: any) => void) | null = null
const runLogsMock = vi.fn()

vi.mock('../../api/client', () => ({
  api: { tasks: { runLogs: (...a: unknown[]) => runLogsMock(...a) } },
}))
vi.mock('../../api/ws', () => ({
  wsClient: { on: (h: (e: any) => void) => { handler = h; return () => { handler = null } } },
}))
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: () => ({ scrollToIndex: vi.fn(), getVirtualItems: () => [], getTotalSize: () => 0, measureElement: vi.fn() }),
}))

import { useRunLogs } from './useRunLogs'

const live = (id: string, content = id) => ({
  type: 'agent.log',
  payload: { task_id: 't', run_id: 'r', entry: { id, type: 'text', content, at: '2024-01-01T00:00:00Z' } },
})

describe('useRunLogs live batching', () => {
  let frames: FrameRequestCallback[]
  beforeEach(() => {
    frames = []
    runLogsMock.mockResolvedValue({ items: [], hasMore: false })
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { frames.push(cb); return frames.length })
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
  })
  afterEach(() => { vi.unstubAllGlobals(); handler = null })

  const flushFrames = () => act(() => { const f = frames; frames = []; f.forEach((cb) => cb(0)) })

  it('buffers a burst into one frame flush and dedupes ids', async () => {
    const { result } = renderHook(() => useRunLogs('t', 'r', true))
    await act(async () => {})
    act(() => { handler!(live('a')); handler!(live('b')); handler!(live('a')) })
    expect(result.current.logs).toHaveLength(0)
    expect(frames).toHaveLength(1)
    flushFrames()
    expect(result.current.logs.map((l) => l.id)).toEqual(['a', 'b'])
    // reconnect duplicate in a later frame
    act(() => { handler!(live('b')); handler!(live('c')) })
    flushFrames()
    expect(result.current.logs.map((l) => l.id)).toEqual(['a', 'b', 'c'])
  })

  it('dedupes against REST page and replay', async () => {
    runLogsMock.mockResolvedValue({
      items: [{ id: 'x', agent_run_id: '', timestamp: '2024-01-01T00:00:00Z', type: 'text', content: 'x' }],
      hasMore: false,
    })
    const { result } = renderHook(() => useRunLogs('t', 'r', true))
    await act(async () => {})
    act(() => {
      handler!(live('x'))
      handler!({ type: 'agent.log_replay', payload: { task_id: 't', run_id: 'r', entries: [{ id: 'y', type: 'text', content: 'y', timestamp: '2024-01-01T00:00:01Z' }] } })
      handler!(live('y'))
    })
    flushFrames()
    expect(result.current.logs.map((l) => l.id).sort()).toEqual(['x', 'y'])
  })
})
