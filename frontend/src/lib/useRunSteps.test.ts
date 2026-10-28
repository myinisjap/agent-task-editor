import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const handlers: ((e: unknown) => void)[] = []
const subscribeTask = vi.fn()
const unsubscribeTask = vi.fn()
vi.mock('../api/ws', () => ({
  wsClient: {
    on: (h: (e: unknown) => void) => {
      handlers.push(h)
      return () => handlers.splice(handlers.indexOf(h), 1)
    },
    subscribeTask: (id: string) => subscribeTask(id),
    unsubscribeTask: (id: string) => unsubscribeTask(id),
  },
}))

import { useRunSteps } from './useRunSteps'

const bash = (command: string) => JSON.stringify({ type: 'assistant', message: { role: 'assistant', content: [{ type: 'tool_use', id: 'x', name: 'Bash', input: { command } }] } })
const entry = (type: string, content: string) => ({ id: '1', type, content, at: '2026-01-01T00:00:00Z' })
const emit = (e: unknown) => act(() => handlers.forEach((h) => h(e)))

beforeEach(() => { handlers.length = 0; subscribeTask.mockClear(); unsubscribeTask.mockClear() })

describe('useRunSteps', () => {
  it('subscribes once per task and shows the latest tool call', () => {
    const runs = [{ run_id: 'r1', task_id: 't1' }, { run_id: 'r2', task_id: 't1' }]
    const { result } = renderHook(() => useRunSteps(runs))
    expect(subscribeTask).toHaveBeenCalledTimes(1)
    emit({ type: 'agent.log', payload: { run_id: 'r1', task_id: 't1', entry: entry('tool_call', bash('go test ./...')) } })
    expect(result.current.r1).toEqual({ tool: 'Bash', summary: 'go test ./...' })
    emit({ type: 'agent.log', payload: { run_id: 'r1', task_id: 't1', entry: entry('stdout', 'hi') } })
    expect(result.current.r1.summary).toBe('go test ./...')
    emit({ type: 'agent.log', payload: { run_id: 'zzz', task_id: 't1', entry: entry('tool_call', bash('x')) } })
    expect(result.current.zzz).toBeUndefined()
  })

  it('replay uses the last tool call', () => {
    const { result } = renderHook(() => useRunSteps([{ run_id: 'r1', task_id: 't1' }]))
    emit({ type: 'agent.log_replay', payload: { run_id: 'r1', task_id: 't1', has_more: false, entries: [entry('tool_call', bash('a')), entry('tool_call', bash('b')), entry('stdout', 'z')] } })
    expect(result.current.r1.summary).toBe('b')
  })

  it('unsubscribes removed runs and on unmount', () => {
    const { result, rerender, unmount } = renderHook(({ runs }) => useRunSteps(runs), {
      initialProps: { runs: [{ run_id: 'r1', task_id: 't1' }, { run_id: 'r2', task_id: 't2' }] },
    })
    emit({ type: 'agent.log', payload: { run_id: 'r2', task_id: 't2', entry: entry('tool_call', bash('a')) } })
    rerender({ runs: [{ run_id: 'r1', task_id: 't1' }] })
    expect(unsubscribeTask).toHaveBeenCalledWith('t2')
    expect(result.current.r2).toBeUndefined()
    unmount()
    expect(unsubscribeTask).toHaveBeenCalledWith('t1')
    expect(handlers.length).toBe(0)
  })
})
