import type { NeedsMeItem, PausedProvider, Task, Workflow } from '../api/client'
import { escalationInfo } from './escalation'
import { humanActionsFor } from './humanGate'
import type { BadgeTone } from '../components/ui'

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

/**
 * Inbox sections, named by what the human does. This is the single source of
 * truth for classification and for the shared count:
 *  - decide: human gates (approve / request changes) and held source-issue edits.
 *  - answer: waiting_human runs whose escalation takes a written reply
 *    (agent_question, rework_loop, and unknown/missing codes).
 *  - fix:    waiting_human runs whose escalation needs a fix or retry
 *    (auth, toolchain, budget, ...), PR conflicts, failed side effects and
 *    paused providers.
 */
export type InboxSectionKey = 'decide' | 'answer' | 'fix'

export type InboxEntry =
  | { kind: 'needs_me'; item: NeedsMeItem }
  | { kind: 'source_edit'; edit: SourceEditTask }
  | { kind: 'side_effect'; failed: FailedEffectTask }
  | { kind: 'provider'; provider: PausedProvider }

export type InboxSections = Record<InboxSectionKey, InboxEntry[]>

export const SECTION_ORDER: InboxSectionKey[] = ['decide', 'answer', 'fix']

/** Per-section display data. Adding a section means adding an entry here (and to SECTION_ORDER). */
export const SECTION_META: Record<string, { title: string; subtitle: string; tone: BadgeTone }> = {
  decide: { title: 'Decide', subtitle: 'Approve or request changes', tone: 'accent' },
  answer: { title: 'Answer', subtitle: 'The agent asked something', tone: 'info' },
  fix: { title: 'Fix setup', subtitle: 'Something needs fixing before work can continue', tone: 'warning' },
}

/** Opens the Needs-you review popup on the first entry. */
export const REVIEW_QUEUE_HREF = '/?review=1'

/** Link that opens the review popup on an entry. */
export function reviewHref(key: string): string {
  return `/?task=${encodeURIComponent(key)}`
}

/** Section keys in display order: known ones first, then any extras. */
export function orderedSectionKeys(s: InboxSections): string[] {
  const extra = Object.keys(s).filter((k) => !(SECTION_ORDER as string[]).includes(k))
  return [...SECTION_ORDER, ...extra]
}

/** Which section a needs-me item belongs in. */
export function sectionFor(item: NeedsMeItem): InboxSectionKey {
  if (item.reason === 'human_gate') return 'decide'
  if (item.reason === 'pr_conflict') return 'fix'
  return escalationInfo(item.escalation_reason).action === 'reply' ? 'answer' : 'fix'
}

export function entrySince(e: InboxEntry): string {
  switch (e.kind) {
    case 'needs_me': return e.item.since
    case 'source_edit': return e.edit.since
    case 'side_effect': return e.failed.since
    case 'provider': return e.provider.paused_at
  }
}

/** Buckets every kind of thing waiting on the human; oldest first within a section. */
export function buildInbox(
  needsMe: NeedsMeItem[],
  failed: FailedEffectTask[],
  paused: PausedProvider[],
  sourceEdits: SourceEditTask[] = [],
): InboxSections {
  const s: InboxSections = { decide: [], answer: [], fix: [] }
  for (const item of needsMe) s[sectionFor(item)].push({ kind: 'needs_me', item })
  for (const edit of sourceEdits) s.decide.push({ kind: 'source_edit', edit })
  for (const f of failed) s.fix.push({ kind: 'side_effect', failed: f })
  for (const provider of paused) s.fix.push({ kind: 'provider', provider })
  for (const k of SECTION_ORDER) s[k].sort((a, b) => entrySince(a).localeCompare(entrySince(b)))
  return s
}

/** The one count: nav badge, tab badge, Home and Inbox heading all use this. */
export function inboxCount(s: InboxSections): number {
  return s.decide.length + s.answer.length + s.fix.length
}

export type ItemActions = {
  approve: boolean
  /** Send the work back with a written note (human `failure` transition). */
  requestChanges: boolean
  /** Re-dispatch the task as is. */
  retry: boolean
  /** The reason needs a written answer, done on the task page. */
  reply: boolean
  fix?: { to: string; text: string }
}

/** Which one-click actions an inbox item offers. */
export function itemActions(item: NeedsMeItem, workflow: Workflow | undefined): ItemActions {
  if (item.reason === 'pr_conflict') return { approve: false, requestChanges: false, retry: false, reply: false }
  const { canApprove, canReject } = humanActionsFor(workflow, item.label)
  if (item.reason === 'human_gate') return { approve: canApprove, requestChanges: canReject, retry: false, reply: false }
  // waiting_human
  const esc = escalationInfo(item.escalation_reason)
  if (canApprove) return { approve: true, requestChanges: false, retry: false, reply: esc.action === 'reply' }
  if (esc.action === 'reply') return { approve: false, requestChanges: false, retry: false, reply: true }
  return { approve: false, requestChanges: false, retry: true, reply: false, fix: esc.action === 'fix' ? esc.fix : undefined }
}

/** Stable selection key for an entry; also what `?task=` carries. */
export function entryKey(e: InboxEntry): string {
  switch (e.kind) {
    case 'needs_me': return e.item.task_id
    case 'source_edit': return `${e.edit.task.id}:source`
    case 'side_effect': return `${e.failed.task.id}:effects`
    case 'provider': return `provider:${e.provider.provider}`
  }
}

export function entryTaskId(e: InboxEntry): string | undefined {
  switch (e.kind) {
    case 'needs_me': return e.item.task_id
    case 'source_edit': return e.edit.task.id
    case 'side_effect': return e.failed.task.id
    case 'provider': return undefined
  }
}

/** Entries in on-screen order (section order, then within-section order). */
export function flattenInbox(s: InboxSections): InboxEntry[] {
  const rec = s as unknown as Record<string, InboxEntry[] | undefined>
  return orderedSectionKeys(s).flatMap((k) => rec[k] ?? [])
}

/** Resolves a `?task=` value to an entry key: exact key, then by task id, then the first entry. */
export function resolveSelection(flat: InboxEntry[], param: string | null): string | undefined {
  if (flat.length === 0) return undefined
  if (param) {
    const exact = flat.find((e) => entryKey(e) === param)
    if (exact) return entryKey(exact)
    const byTask = flat.find((e) => entryTaskId(e) === param)
    if (byTask) return entryKey(byTask)
  }
  return entryKey(flat[0])
}

/** The key one step away, clamped to the list (no wrap). */
export function neighborKey(flat: InboxEntry[], key: string | undefined, delta: 1 | -1): string | undefined {
  if (flat.length === 0) return undefined
  const i = flat.findIndex((e) => entryKey(e) === key)
  if (i < 0) return entryKey(flat[0])
  return entryKey(flat[Math.min(flat.length - 1, Math.max(0, i + delta))])
}

/** Where selection goes once `key` is gone: the next entry, else the previous one. */
export function nextAfterRemoval(flat: InboxEntry[], key: string | undefined): string | undefined {
  const i = flat.findIndex((e) => entryKey(e) === key)
  if (i < 0) return undefined
  const n = flat[i + 1] ?? flat[i - 1]
  return n ? entryKey(n) : undefined
}
