import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useInboxCount, useInboxSections, useInboxStore } from './inbox'
import { useNeedsMeStore } from './needsMe'
import { useHealthStore } from './health'

beforeEach(() => {
  useNeedsMeStore.setState({ items: [], byTask: {} })
  useInboxStore.setState({ failedEffects: [], sourceEdits: [], loaded: true })
  useHealthStore.setState({ checks: [], paused: [], loaded: true })
})

describe('useInboxCount', () => {
  it('counts needs-me, failed effects, source edits and paused providers, matching the sections', () => {
    useNeedsMeStore.setState({
      items: [{ task_id: 'a', task_title: 'A', repo_id: 'r', workflow_id: 'w', label: 'l', reason: 'human_gate', since: '2026-01-01T00:00:00Z' }],
      byTask: {},
    } as never)
    useInboxStore.setState({
      failedEffects: [{ task: { id: 'f' }, effects: [], since: '2026-01-01T00:00:00Z' }],
      sourceEdits: [{ task: { id: 's' }, since: '2026-01-01T00:00:00Z' }],
    } as never)
    useHealthStore.setState({ paused: [{ provider: 'claude', reason: 'x', paused_at: '2026-01-01T00:00:00Z' }] } as never)
    const { result } = renderHook(() => ({ n: useInboxCount(), s: useInboxSections() }))
    expect(result.current.n).toBe(4)
    expect(result.current.s.decide.length + result.current.s.answer.length + result.current.s.fix.length).toBe(4)
  })
})
