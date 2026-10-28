import { describe, it, expect } from 'vitest'
import type { AutonomySuggestions, ShouldHaveAskedResponse, TaskLabelHistoryEntry } from '../api/client'
import {
  backtestLine, backtestSummary, buildRule, conditionOptions, lastGateExitByPolicy, lessonPrefill, optionText, successMessage,
} from './shouldHaveAsked'

const sugg = {
  changed_files: 6, changed_lines: 200,
  path_globs: [{ glob: 'backend/internal/remotemcp/**', kind: 'dir', files: 4 }, { glob: '**/*.go', kind: 'ext', files: 5 }],
  task_type: 'feature', min_changed_lines: 150,
} as unknown as AutonomySuggestions

describe('conditionOptions', () => {
  it('labels each suggestion', () => {
    const t = conditionOptions(sugg).map(optionText)
    expect(t).toEqual([
      'Changes under backend/internal/remotemcp/, 4 of 6 files',
      'Changes to **/*.go files, 5 of 6 files',
      'Any feature task',
      'More than 150 lines changed',
    ])
  })
  it('omits absent suggestions', () => {
    expect(conditionOptions({ ...sugg, path_globs: [], task_type: null, min_changed_lines: null })).toEqual([])
  })
})

describe('buildRule', () => {
  const opts = conditionOptions(sugg)
  it('is undefined when nothing is ticked', () => expect(buildRule([])).toBeUndefined())
  it('merges everything into one rule', () => {
    expect(buildRule(opts)).toEqual({ path_globs: ['backend/internal/remotemcp/**', '**/*.go'], task_type: 'feature', min_changed_lines: 150 })
  })
  it('omits path_globs when none ticked and adds repo_id on request', () => {
    expect(buildRule([opts[2]], 'r1')).toEqual({ task_type: 'feature', repo_id: 'r1' })
  })
})

describe('lessonPrefill', () => {
  it('names globs and title, capped at 300', () => {
    expect(lessonPrefill(['a/**', 'b/**'], 'T')).toContain('under a/**, b/**')
    expect(lessonPrefill(['x'.repeat(400)], 'T')).toHaveLength(300)
  })
})

function mk(o: Record<string, unknown>) {
  return { policy_approvals: 10, evaluated: 10, unknown: 0, would_stop: 3, stopped: [], ...o } as never
}

describe('backtest', () => {
  it('excludes the current task and sums gates', () => {
    const a = mk({ stopped: [{ task_id: 'me' }, { task_id: 'o' }] })
    const b = mk({ policy_approvals: 5, would_stop: 1, unknown: 2 })
    expect(backtestSummary([a, b], 'me')).toEqual({ n: 3, m: 14, unknown: 2 })
  })
  it('lines', () => {
    expect(backtestLine({ n: 2, m: 9, unknown: 0 })).toBe('This rule would also have stopped 2 of the 9 tasks auto-approved in the last 30 days')
    expect(backtestLine({ n: 0, m: 1, unknown: 1 })).toBe("This rule would also have stopped 0 of the 1 task auto-approved in the last 30 days. 1 couldn't be checked.")
    expect(backtestLine({ n: 0, m: 0, unknown: 0 })).toBe('No other tasks were auto-approved in the last 30 days.')
  })
})

describe('successMessage', () => {
  const base = { rule: null, lesson: null, sent_back: false, step_down: null } as unknown as ShouldHaveAskedResponse
  it('log only', () => expect(successMessage(base)).toEqual({ message: 'Logged it.', error: false }))
  it('everything', () => {
    const r = { ...base, rule: { summary: 'under x' }, lesson: {}, sent_back: true, step_down: { label: 'review', to_mode: 'ask' } } as unknown as ShouldHaveAskedResponse
    expect(successMessage(r, 'Review').message).toBe('Added ask rule: under x, saved a lesson, sent back to work. Review stepped down to ask.')
  })
  it('send-back error is an error', () => {
    const r = { ...base, send_back_error: 'boom' } as ShouldHaveAskedResponse
    expect(successMessage(r)).toEqual({ message: "Logged it. Couldn't send it back: boom", error: true })
  })
})

describe('lastGateExitByPolicy', () => {
  const e = (trigger: string, actor: string | null, at: string) => ({ trigger, actor_id: actor, created_at: at }) as TaskLabelHistoryEntry
  it('looks at the newest human entry', () => {
    expect(lastGateExitByPolicy([e('human', 'policy', '2026-01-02T00:00:00Z'), e('human', null, '2026-01-01T00:00:00Z'), e('agent', 'r', '2026-01-03T00:00:00Z')])).toBe(true)
    expect(lastGateExitByPolicy([e('human', null, '2026-01-02T00:00:00Z'), e('human', 'policy', '2026-01-01T00:00:00Z')])).toBe(false)
    expect(lastGateExitByPolicy([])).toBe(false)
  })
})
