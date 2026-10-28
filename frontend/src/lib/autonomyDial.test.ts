import { describe, it, expect } from 'vitest'
import type { AskRule, AutonomyBacktest, Repo, Workflow } from '../api/client'
import {
  backtestSummary,
  backtestVerdict,
  gateHasDiff,
  isAutonomyGate,
  isRuleUnused,
  passCategory,
  reposWithoutVerify,
  toLabelAutonomy,
  withGateAutonomy,
  withMode,
  DEFAULT_ASK_ON,
} from './autonomyDial'

const L = (name: string, o: Record<string, unknown> = {}, i = 0) => ({
  id: name, workflow_id: 'w', name, color: '#111', sort_order: i, agent_ignore: 0, is_terminal: 0,
  create_pr: 0, wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '', ...o,
})
const T = (from: string, to: string, trigger: string, path: string | null = null, extra = {}) => ({
  id: `${from}-${to}`, workflow_id: 'w', from_label: from, to_label: to, trigger_type: trigger, path, ...extra,
})
const wf = {
  id: 'w', name: 'Default', description: '', created_at: '', updated_at: 'u',
  labels: [
    L('plan', {}, 0), L('review-plan', {}, 1), L('work', { create_pr: 1 }, 2), L('review', { autonomy: { mode: 'auto', ask_on: [], hold_seconds: 600 } }, 3),
    L('not_ready', { agent_ignore: 1 }, 4), L('done', { is_terminal: 1 }, 5),
  ],
  transitions: [
    T('plan', 'review-plan', 'agent', 'success'),
    T('review-plan', 'work', 'human', 'success'),
    T('work', 'review', 'agent', 'success', { agent_config_id: 'ag1' }),
    T('review', 'done', 'human', 'success'),
    T('review', 'work', 'human', 'failure'),
    T('not_ready', 'plan', 'human', 'success'),
  ],
} as unknown as Workflow

const bt = (o: Partial<AutonomyBacktest>): AutonomyBacktest => ({
  total: 0, evaluated: 0, unknown: 0, would_skip: 0, would_ask: 0, rejected: 0, escaped: 0,
  truncated: false, timed_out: false, passes: [], rules: null, ...o,
})

describe('autonomyDial', () => {
  it('isAutonomyGate follows the backend rule', () => {
    expect(isAutonomyGate(wf, 'review')).toBe(true)
    expect(isAutonomyGate(wf, 'not_ready')).toBe(false)
    expect(isAutonomyGate(wf, 'done')).toBe(false)
    expect(isAutonomyGate(wf, 'work')).toBe(false)
  })

  it('gateHasDiff: plan review has none, code review has', () => {
    expect(gateHasDiff(wf, 'review-plan')).toBe(false)
    expect(gateHasDiff(wf, 'review')).toBe(true)
  })

  it('passCategory', () => {
    const p = { task_id: 't', title: 't', at: '', flags: [] }
    expect(passCategory({ ...p, outcome: 'approved', would_skip: false, unknown: false })).toBe('asks')
    expect(passCategory({ ...p, outcome: 'approved', would_skip: true, unknown: false })).toBe('auto')
    expect(passCategory({ ...p, outcome: 'rejected', would_skip: false, unknown: false })).toBe('caught')
    expect(passCategory({ ...p, outcome: 'rejected', would_skip: true, unknown: false })).toBe('slipped')
    expect(passCategory({ ...p, outcome: 'rejected', would_skip: null, unknown: true })).toBe('unknown')
  })

  it('backtestVerdict branches', () => {
    expect(backtestVerdict(bt({}))).toMatch(/No passes/)
    expect(backtestVerdict(bt({ evaluated: 5, escaped: 2 }))).toBe('Would have let 2 rejected tasks through without review.')
    expect(backtestVerdict(bt({ evaluated: 5, would_skip: 3 }))).toBe('Would have skipped 3 of 5 reviews and missed no rejections.')
    expect(backtestVerdict(bt({ evaluated: 5, truncated: true, timed_out: true }))).toBe(
      'Would still have asked you every time. (only the latest 500 passes) (partial: timed out)',
    )
    expect(backtestSummary(bt({ evaluated: 40, would_skip: 12 }))).toBe('Last 30 days: would skip 12 of 40 · 0 rejections missed')
  })

  it('isRuleUnused', () => {
    const now = new Date('2025-06-01T00:00:00Z')
    const r = { last_hit_at: null } as AskRule
    expect(isRuleUnused(r, now)).toBe(true)
    expect(isRuleUnused({ last_hit_at: '2025-05-20T00:00:00Z' } as AskRule, now)).toBe(false)
    expect(isRuleUnused({ last_hit_at: '2025-03-01T00:00:00Z' } as AskRule, now)).toBe(true)
  })

  it('reposWithoutVerify', () => {
    const repos = [
      { id: 'a', name: 'a', workflow_id: null, verify_command: '' },
      { id: 'b', name: 'b', workflow_id: 'w', verify_command: 'make test' },
      { id: 'c', name: 'c', workflow_id: 'other', verify_command: '' },
      { id: 'd', name: 'd', workflow_id: 'w', verify_command: '' },
    ] as unknown as Repo[]
    expect(reposWithoutVerify(wf, repos, [wf]).map((r) => r.name)).toEqual(['a', 'd'])
  })

  it('withGateAutonomy keeps everything else', () => {
    const doc = JSON.parse(withGateAutonomy(wf, 'review-plan', { mode: 'auto', ask_on: [], hold_seconds: 0 }))
    const byName = (n: string) => doc.labels.find((l: { name: string }) => l.name === n)
    expect(byName('review-plan').autonomy.mode).toBe('auto')
    expect(byName('review').autonomy.mode).toBe('auto')
    expect(byName('work').create_pr).toBe(true)
    expect(doc.transitions.find((t: { from: string; to: string }) => t.from === 'work').agent_config).toBe('ag1')
    expect(doc.transitions).toHaveLength(6)
    const off = JSON.parse(withGateAutonomy(wf, 'review', null))
    expect(off.labels.find((l: { name: string }) => l.name === 'review').autonomy).toBeUndefined()
  })

  it('withMode pre-checks defaults and toLabelAutonomy nulls ask', () => {
    const f = withMode({ mode: 'ask', ask_on: [], hold_seconds: 600, max_cost_usd: null }, 'on_flags')
    expect(f.ask_on).toEqual(DEFAULT_ASK_ON)
    expect(toLabelAutonomy({ ...f, mode: 'ask' })).toBeNull()
  })
})
