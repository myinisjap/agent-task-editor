import type {
  AskRuleInput,
  AutonomyBacktest,
  AutonomySuggestions,
  ShouldHaveAskedResponse,
  TaskLabelHistoryEntry,
} from '../api/client'
import { LESSON_BODY_MAX } from './lessons'

export type ConditionOption = {
  id: string
  kind: 'glob' | 'type' | 'size'
  /** Plain text before the highlighted code part. */
  prefix: string
  /** Rendered in <code> (a path or glob). */
  code?: string
  /** Plain text after the code part. */
  suffix: string
  glob?: string
  taskType?: string
  minLines?: number
}

/** Visible text of an option, without markup. */
export function optionText(o: ConditionOption): string {
  return `${o.prefix}${o.code ?? ''}${o.suffix}`
}

const filesOf = (n: number, total: number) => `${n} of ${total} ${total === 1 ? 'file' : 'files'}`

/** Checkbox options for the "What should have stopped it?" group. */
export function conditionOptions(s: AutonomySuggestions): ConditionOption[] {
  const out: ConditionOption[] = []
  for (const g of s.path_globs) {
    if (g.kind === 'dir') {
      out.push({
        id: `glob:${g.glob}`, kind: 'glob', glob: g.glob,
        prefix: 'Changes under ', code: g.glob.replace(/\*+$/, ''), suffix: `, ${filesOf(g.files, s.changed_files)}`,
      })
    } else {
      out.push({
        id: `glob:${g.glob}`, kind: 'glob', glob: g.glob,
        prefix: 'Changes to ', code: g.glob, suffix: ` files, ${filesOf(g.files, s.changed_files)}`,
      })
    }
  }
  if (s.task_type) {
    out.push({ id: 'type', kind: 'type', taskType: s.task_type, prefix: `Any ${s.task_type} task`, suffix: '' })
  }
  if (s.min_changed_lines != null) {
    out.push({ id: 'size', kind: 'size', minLines: s.min_changed_lines, prefix: `More than ${s.min_changed_lines} lines changed`, suffix: '' })
  }
  return out
}

/** Merges every ticked condition into one rule (all conditions must match; ticked globs are OR'd). */
export function buildRule(selected: ConditionOption[], repoId?: string | null): AskRuleInput | undefined {
  if (selected.length === 0) return undefined
  const rule: AskRuleInput = {}
  const globs = selected.filter((o) => o.kind === 'glob' && o.glob).map((o) => o.glob as string)
  if (globs.length > 0) rule.path_globs = globs
  const type = selected.find((o) => o.kind === 'type')
  if (type?.taskType) rule.task_type = type.taskType
  const size = selected.find((o) => o.kind === 'size')
  if (size?.minLines != null) rule.min_changed_lines = size.minLines
  if (repoId) rule.repo_id = repoId
  return rule
}

export function lessonPrefill(globs: string[], title: string): string {
  return `Ask a person to review before merging changes under ${globs.join(', ')}. "${title}" was auto-approved and should not have been.`.slice(0, LESSON_BODY_MAX)
}

export type BacktestSummary = { n: number; m: number; unknown: number }

/** Sums backtest results across gates, leaving out the task being flagged. */
export function backtestSummary(results: AutonomyBacktest['rules'][], taskId: string): BacktestSummary {
  let n = 0, m = 0, unknown = 0
  for (const r of results) {
    if (!r) continue
    n += r.would_stop
    m += r.policy_approvals
    unknown += r.unknown
    if (r.stopped.some((s) => s.task_id === taskId)) { n -= 1; m -= 1 }
  }
  return { n: Math.max(0, n), m: Math.max(0, m), unknown }
}

export function backtestLine({ n, m, unknown }: BacktestSummary): string {
  if (m === 0) return 'No other tasks were auto-approved in the last 30 days.'
  const base = `This rule would also have stopped ${n} of the ${m} ${m === 1 ? 'task' : 'tasks'} auto-approved in the last 30 days`
  return unknown > 0 ? `${base}. ${unknown} couldn't be checked.` : base
}

export function successMessage(resp: ShouldHaveAskedResponse, gateName?: string): { message: string; error: boolean } {
  let msg = resp.rule ? `Added ask rule: ${resp.rule.summary}` : 'Logged it'
  if (resp.lesson) msg += ', saved a lesson'
  if (resp.sent_back) msg += ', sent back to work'
  msg += '.'
  if (resp.step_down) {
    msg += ` ${gateName ?? resp.step_down.label} stepped down to ${resp.step_down.to_mode}.`
  }
  if (resp.send_back_error) msg += ` Couldn't send it back: ${resp.send_back_error}`
  return { message: msg, error: !!resp.send_back_error }
}

/** True when the newest human-triggered label change was made by the autonomy policy. */
export function lastGateExitByPolicy(history: TaskLabelHistoryEntry[]): boolean {
  let newest: TaskLabelHistoryEntry | undefined
  for (const e of history) {
    if (e.trigger !== 'human') continue
    if (!newest || new Date(e.created_at).getTime() > new Date(newest.created_at).getTime()) newest = e
  }
  return newest?.actor_id === 'policy'
}
