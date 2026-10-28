import { describe, it, expect } from 'vitest'
import type { Workflow } from '../api/client'
import type { InboxEntry, InboxSections } from './inbox'
import { entryHref, entryTitle, formatCompactAge, formatElapsed, homeStations, isStale, labelStations, nonEmptySections, oldestSince, sectionTitle } from './homeInbox'

const t = (id: string, title: string) => ({ id, title })
const needs = { kind: 'needs_me', item: { task_id: 'a', task_title: 'Needs', since: '2026-01-01T00:00:00Z', reason: 'human_gate' } } as unknown as InboxEntry
const edit = { kind: 'source_edit', edit: { task: t('b', 'Edited'), since: '2026-01-02T00:00:00Z' } } as unknown as InboxEntry
const failed = { kind: 'side_effect', failed: { task: t('c', 'Failed'), effects: [], since: '2026-01-03T00:00:00Z' } } as unknown as InboxEntry
const prov = { kind: 'provider', provider: { provider: 'claude', reason: 'x', paused_at: '2026-01-04T00:00:00Z' } } as unknown as InboxEntry

describe('entry helpers', () => {
  it('titles all kinds and falls back for unknown', () => {
    expect(entryTitle(needs)).toBe('Needs')
    expect(entryTitle(edit)).toBe('Edited')
    expect(entryTitle(failed)).toBe('Failed')
    expect(entryTitle(prov)).toBe('claude paused')
    expect(entryTitle({ kind: 'weird' } as unknown as InboxEntry)).toBe('Item')
  })
  it('links to the task or the inbox', () => {
    expect(entryHref(needs)).toBe('/tasks/a')
    expect(entryHref(prov)).toBe('/inbox?task=provider%3Aclaude')
  })
  it('section titles with fallback', () => {
    expect(sectionTitle('fix')).toBe('Fix setup')
    expect(sectionTitle('auto_review')).toBe('Auto review')
    expect(sectionTitle('')).toBe('Other')
  })
  it('walks unknown sections and finds the oldest', () => {
    const s = { decide: [edit], answer: [], fix: [prov], extra: [needs] } as unknown as InboxSections
    expect(nonEmptySections(s).map(([k]) => k)).toEqual(['decide', 'fix', 'extra'])
    expect(oldestSince(s)).toBe('2026-01-01T00:00:00Z')
    expect(oldestSince({ decide: [], answer: [], fix: [] })).toBeUndefined()
  })
})

describe('time formatting', () => {
  const now = new Date('2026-01-10T12:00:00Z').getTime()
  it('compact ages', () => {
    expect(formatCompactAge('2026-01-10T11:59:50Z', now)).toBe('<1m')
    expect(formatCompactAge('2026-01-10T11:19:00Z', now)).toBe('41m')
    expect(formatCompactAge('2026-01-10T10:00:00Z', now)).toBe('2h')
    expect(formatCompactAge('2026-01-07T12:00:00Z', now)).toBe('3d')
  })
  it('stale after 30m', () => {
    expect(isStale('2026-01-10T11:29:00Z', now)).toBe(true)
    expect(isStale('2026-01-10T11:45:00Z', now)).toBe(false)
  })
  it('elapsed', () => {
    expect(formatElapsed(65_000)).toBe('1:05')
    expect(formatElapsed(3_725_000)).toBe('1:02:05')
    expect(formatElapsed(-5)).toBe('0:00')
  })
})

const NAMES = ['agent-review', 'done', 'not_ready', 'plan', 'review', 'review-plan', 'testing', 'work']
const mk = (names: string[], transitions: { from_label: string; trigger_type: string }[]): Workflow => ({
  id: 'w', name: 'w', description: '', created_at: '', updated_at: '',
  labels: names.map((name, i) => ({
    id: `id-${name}`, workflow_id: 'w', name, color: '#123456', sort_order: i,
    agent_ignore: name === 'not_ready' ? 1 : 0, is_terminal: name === 'done' ? 1 : 0,
  })),
  transitions,
}) as unknown as Workflow

describe('homeStations', () => {
  it('flags human gates per label on the default workflow', () => {
    const w = mk(NAMES, [
      { from_label: 'review', trigger_type: 'human' },
      { from_label: 'work', trigger_type: 'agent' },
    ])
    const st = homeStations(w, { review: 2 })
    expect(st.find((s) => s.name === 'Review')).toMatchObject({ isGate: true, count: 2 })
    expect(st.find((s) => s.name === 'Work')?.isGate).toBe(false)
    expect(st.find((s) => s.name === 'Done')?.isGate).toBe(false)
    expect(labelStations(w, {}).find((s) => s.name === 'Not ready')?.isGate).toBe(true)
  })
  it('uses the waiting-on-human bucket for custom workflows', () => {
    const w = mk(['todo', 'doing', 'signoff', 'shipped'], [
      { from_label: 'doing', trigger_type: 'agent' },
      { from_label: 'signoff', trigger_type: 'human' },
    ])
    const st = homeStations(w, { signoff: 3 })
    expect(st).toHaveLength(3)
    expect(st.filter((s) => s.isGate).map((s) => s.key)).toEqual(['waitingHuman'])
    expect(st.find((s) => s.key === 'waitingHuman')?.count).toBeGreaterThan(0)
  })
})
