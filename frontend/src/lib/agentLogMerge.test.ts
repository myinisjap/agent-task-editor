import { describe, it, expect } from 'vitest'
import { toLog, mergeLogs } from './agentLogMerge'

describe('toLog', () => {
  it('preserves a server-provided id', () => {
    const l = toLog({ id: 'row-123', type: 'stdout', content: 'hello', at: '2024-01-01T00:00:00Z' })
    expect(l.id).toBe('row-123')
    expect(l.timestamp).toBe('2024-01-01T00:00:00Z')
  })

  it('prefers timestamp over at when both are present', () => {
    const l = toLog({ id: 'row-1', type: 'stdout', content: 'x', timestamp: '2024-02-02T00:00:00Z', at: '2024-01-01T00:00:00Z' })
    expect(l.timestamp).toBe('2024-02-02T00:00:00Z')
  })

  it('falls back to a deterministic content-derived id when id is absent', () => {
    const payload = { type: 'stdout', content: 'hello', at: '2024-01-01T00:00:00Z' }
    const a = toLog(payload)
    const b = toLog({ ...payload })
    expect(a.id).toBe(b.id)
    expect(a.id).not.toMatch(/^[0-9a-f-]{36}$/) // not a uuid; deterministic synthetic key
  })

  it('produces different fallback ids for different content', () => {
    const a = toLog({ type: 'stdout', content: 'hello', at: '2024-01-01T00:00:00Z' })
    const b = toLog({ type: 'stdout', content: 'goodbye', at: '2024-01-01T00:00:00Z' })
    expect(a.id).not.toBe(b.id)
  })
})

describe('mergeLogs', () => {
  it('dedupes a live entry against the same-id replay entry', () => {
    const live = [toLog({ id: 'row-1', type: 'stdout', content: 'a', at: '2024-01-01T00:00:00.000Z' })]
    const replay = [toLog({ id: 'row-1', type: 'stdout', content: 'a', timestamp: '2024-01-01T00:00:00.000Z' })]
    const merged = mergeLogs(live, replay)
    expect(merged).toHaveLength(1)
    expect(merged[0].id).toBe('row-1')
  })

  it('sorts merged entries chronologically', () => {
    const a = toLog({ id: 'row-2', type: 'stdout', content: 'second', timestamp: '2024-01-01T00:00:02.000Z' })
    const b = toLog({ id: 'row-1', type: 'stdout', content: 'first', timestamp: '2024-01-01T00:00:01.000Z' })
    const merged = mergeLogs([a], [b])
    expect(merged.map((l) => l.id)).toEqual(['row-1', 'row-2'])
  })

  it('returns prev unchanged when incoming is empty', () => {
    const prev = [toLog({ id: 'row-1', type: 'stdout', content: 'a', timestamp: '2024-01-01T00:00:00.000Z' })]
    expect(mergeLogs(prev, [])).toBe(prev)
  })
})
