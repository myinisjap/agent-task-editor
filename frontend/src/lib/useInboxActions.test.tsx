import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useInboxActions } from './useInboxActions'
import type { InboxEntry } from './inbox'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore } from '../stores/inbox'
import { useHealthStore } from '../stores/health'

const m = { approve: vi.fn(), reject: vi.fn(), retry: vi.fn(), dismiss: vi.fn(), apply: vi.fn(), discard: vi.fn(), rerun: vi.fn(), resume: vi.fn(), reply: vi.fn() }
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      needsMe: { get: () => Promise.resolve({ items: [], approved_by_policy: [] }) },
      tasks: {
        list: () => Promise.resolve({ items: [], nextCursor: null }),
        approve: (...a: unknown[]) => m.approve(...a),
        reject: (...a: unknown[]) => m.reject(...a),
        rerun: (...a: unknown[]) => m.rerun(...a),
        retrySideEffects: (...a: unknown[]) => m.retry(...a),
        dismissSideEffects: (...a: unknown[]) => m.dismiss(...a),
        applySourceUpdate: (...a: unknown[]) => m.apply(...a),
        discardSourceUpdate: (...a: unknown[]) => m.discard(...a),
        replyRun: (...a: unknown[]) => m.reply(...a),
      },
      health: { providers: () => Promise.resolve({ checks: [], paused_providers: [] }), resumeProvider: (...a: unknown[]) => m.resume(...a) },
    },
  }
})

const gate = { kind: 'needs_me', item: { task_id: 't1', task_title: 'T', reason: 'human_gate', since: '2026-01-01T00:00:00Z' } } as unknown as InboxEntry
const answer = { kind: 'needs_me', item: { task_id: 't3', reason: 'waiting_human', run_id: 'run1', since: '2026-01-01T00:00:00Z' } } as unknown as InboxEntry
const fx = { kind: 'side_effect', failed: { task: { id: 't2', title: 'P' }, effects: [], since: '2026-01-01T00:00:00Z' } } as unknown as InboxEntry
const src = { kind: 'source_edit', edit: { task: { id: 't4', title: 'E' }, since: '2026-01-01T00:00:00Z' } } as unknown as InboxEntry
const prov = { kind: 'provider', provider: { provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' } } as unknown as InboxEntry

beforeEach(() => {
  Object.values(m).forEach((f) => { f.mockReset(); f.mockResolvedValue({}) })
  useNeedsMeStore.setState({ items: [], byTask: {}, loaded: false })
  useInboxStore.setState({ failedEffects: [], sourceEdits: [], loaded: false })
  useHealthStore.setState({ checks: [], paused: [], loaded: false })
})

describe('useInboxActions', () => {
  it('approve calls the API and onSuccess with the advance key', async () => {
    const onSuccess = vi.fn()
    const { result } = renderHook(() => useInboxActions({ onSuccess }))
    await act(() => result.current.approve(gate, 'next'))
    expect(m.approve).toHaveBeenCalledWith('t1')
    expect(onSuccess).toHaveBeenCalledWith('next')
  })

  it('request changes sends the trimmed note and closes it', async () => {
    const { result } = renderHook(() => useInboxActions())
    act(() => {
      result.current.openNote('t1')
      result.current.previewProps(gate, undefined, undefined).setNote('  fix it ')
    })
    expect(result.current.isNoteOpen('t1')).toBe(true)
    await act(() => result.current.sendChanges(gate))
    expect(m.reject).toHaveBeenCalledWith('t1', 'fix it')
    expect(result.current.isNoteOpen('t1')).toBe(false)
  })

  it('reply, effects, source edits, provider resume', async () => {
    const { result } = renderHook(() => useInboxActions())
    act(() => result.current.previewProps(answer, undefined, undefined).setReply('PG'))
    await act(() => result.current.sendReply(answer))
    expect(m.reply).toHaveBeenCalledWith('t3', 'run1', 'PG')
    await act(() => result.current.retryEffects(fx))
    expect(m.retry).toHaveBeenCalledWith('t2')
    await act(() => result.current.dismissEffects(fx))
    expect(m.dismiss).toHaveBeenCalledWith('t2')
    await act(() => result.current.applySource(src))
    expect(m.apply).toHaveBeenCalledWith('t4')
    await act(() => result.current.discardSource(src))
    expect(m.discard).toHaveBeenCalledWith('t4')
    await act(() => result.current.resumeProvider(prov))
    expect(m.resume).toHaveBeenCalledWith('claude')
    await act(() => result.current.retry(gate))
    expect(m.rerun).toHaveBeenCalledWith('t1')
  })

  it('records an error under the entry key and skips onSuccess', async () => {
    m.approve.mockRejectedValue(new Error('nope'))
    const onSuccess = vi.fn()
    const { result } = renderHook(() => useInboxActions({ onSuccess }))
    await act(() => result.current.approve(gate, 'n'))
    expect(result.current.previewProps(gate, undefined, undefined).error).toBe('nope')
    expect(onSuccess).not.toHaveBeenCalled()
  })
})
