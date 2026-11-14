import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useWorkflowStore } from './workflow'
import { api } from '../api/client'

describe('workflow store fetch', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useWorkflowStore.setState({ workflows: [], selectedId: null })
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('retries a transient list failure instead of leaving the board empty', async () => {
    const wf = { id: 'w1', labels: [], transitions: [] } as never
    const spy = vi
      .spyOn(api.workflows, 'list')
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce([wf])
    const p = useWorkflowStore.getState().fetch()
    await vi.runAllTimersAsync()
    await p
    expect(spy).toHaveBeenCalledTimes(2)
    expect(useWorkflowStore.getState().workflows).toHaveLength(1)
  })
})
