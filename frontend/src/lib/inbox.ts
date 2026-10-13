import type { NeedsMeItem, PausedProvider, Task, Workflow } from '../api/client'
import { escalationInfo } from './escalation'
import { humanActionsFor } from './humanGate'

type SideEffect = NonNullable<Task['side_effects']>[number]

/** A task whose outbox holds at least one effect that exhausted its retries. */
export type FailedEffectTask = { task: Task; effects: SideEffect[]; since: string }

/** A task whose source issue was edited after a human approved it; the edit is held for review. */
export type SourceEditTask = { task: Task; since: string }

export type InboxGroupKey = 'reply' | 'approval' | 'pr_conflict' | 'side_effect' | 'source_edit' | 'provider'

export type InboxGroups = {
  reply: NeedsMeItem[]
  approval: NeedsMeItem[]
  pr_conflict: NeedsMeItem[]
  side_effect: FailedEffectTask[]
  source_edit: SourceEditTask[]
  provider: PausedProvider[]
}

/** Tasks that have at least one permanently failed side-effect row, oldest failure first. */
export function failedEffectTasks(tasks: Task[]): FailedEffectTask[] {
  const out: FailedEffectTask[] = []
  for (const task of tasks) {
    if (task.archived) continue
    const effects = (task.side_effects ?? []).filter((e) => e.status === 'failed')
    if (effects.length === 0) continue
    // next_at is the last scheduled attempt; the best "since" the API exposes.
    const since = effects.map((e) => e.next_at).sort()[0]
    out.push({ task, effects, since })
  }
  return out.sort((a, b) => a.since.localeCompare(b.since))
}

/** Tasks holding an issue edit that arrived after approval, oldest first. */
export function sourceEditTasks(tasks: Task[]): SourceEditTask[] {
  const out: SourceEditTask[] = []
  for (const task of tasks) {
    if (task.archived || !task.source_pending_update) continue
    out.push({ task, since: task.source_pending_update.created_at })
  }
  return out.sort((a, b) => a.since.localeCompare(b.since))
}

/** Groups every kind of thing waiting on the human. Order inside a group is preserved. */
export function buildInbox(
  needsMe: NeedsMeItem[],
  failed: FailedEffectTask[],
  paused: PausedProvider[],
  sourceEdits: SourceEditTask[] = [],
): InboxGroups {
  return {
    reply: needsMe.filter((i) => i.reason === 'waiting_human'),
    approval: needsMe.filter((i) => i.reason === 'human_gate'),
    pr_conflict: needsMe.filter((i) => i.reason === 'pr_conflict'),
    side_effect: failed,
    source_edit: sourceEdits,
    provider: paused,
  }
}

export function inboxCount(g: InboxGroups): number {
  return g.reply.length + g.approval.length + g.pr_conflict.length + g.side_effect.length + g.source_edit.length + g.provider.length
}

export type ItemActions = {
  approve: boolean
  /** Re-dispatch the task as is. */
  retry: boolean
  /** The reason needs a written answer, done on the task page. */
  reply: boolean
  fix?: { to: string; text: string }
}

/** Which one-click actions an inbox item offers, mirroring the Dashboard queue. */
export function itemActions(item: NeedsMeItem, workflow: Workflow | undefined): ItemActions {
  if (item.reason === 'pr_conflict') return { approve: false, retry: false, reply: false }
  const { canApprove } = humanActionsFor(workflow, item.label)
  if (item.reason === 'human_gate') return { approve: canApprove, retry: false, reply: false }
  // waiting_human
  const esc = escalationInfo(item.escalation_reason)
  if (canApprove) return { approve: true, retry: false, reply: esc.action === 'reply' }
  if (esc.action === 'reply') return { approve: false, retry: false, reply: true }
  return { approve: false, retry: true, reply: false, fix: esc.action === 'fix' ? esc.fix : undefined }
}
