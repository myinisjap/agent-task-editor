// Pure helpers for the Home control room: inbox row text, compact ages and
// the station list (with human-gate flags) behind the scene legend/strip.
import type { Workflow } from '../api/client'
import { bucketize } from '../components/taskBuckets'
import { stationsFor } from '../components/factoryStations'
import { entryKey, entrySince, orderedSectionKeys, reviewHref, SECTION_META, type InboxEntry, type InboxSections } from './inbox'
import { isHumanGateLabel } from './humanGate'
import { labelDisplayName } from './labelDisplay'

/** Row title; unknown kinds fall back to a generic string instead of crashing. */
export function entryTitle(e: InboxEntry): string {
  switch (e.kind) {
    case 'needs_me': return e.item.task_title
    case 'source_edit': return e.edit.task.title
    case 'side_effect': return e.failed.task.title
    case 'provider': return `${e.provider.provider} paused`
    default: return 'Item'
  }
}

/** Where a row goes: Home with the review popup open on the entry. */
export function entryHref(e: InboxEntry): string {
  return reviewHref(entryKey(e))
}

/** Section heading; unknown sections get a capitalised key, or "Other". */
export function sectionTitle(key: string): string {
  if (SECTION_META[key]) return SECTION_META[key].title
  const k = key.replace(/[-_]+/g, ' ').trim()
  return k ? k.charAt(0).toUpperCase() + k.slice(1) : 'Other'
}

/** Section keys in display order: known ones first, then any extras. */
export function sectionKeys(sections: InboxSections): string[] {
  return orderedSectionKeys(sections)
}

/** [sectionKey, entries] for every non-empty section, in display order. */
export function nonEmptySections(sections: InboxSections): [string, InboxEntry[]][] {
  const rec = sections as unknown as Record<string, InboxEntry[] | undefined>
  return sectionKeys(sections)
    .map((k): [string, InboxEntry[]] => [k, rec[k] ?? []])
    .filter(([, v]) => v.length > 0)
}

/** Oldest `since` (ISO) across all entries, or undefined when empty. */
export function oldestSince(sections: InboxSections): string | undefined {
  let oldest: string | undefined
  for (const [, entries] of nonEmptySections(sections)) {
    for (const e of entries) {
      const s = entrySince(e)
      if (s && (!oldest || s < oldest)) oldest = s
    }
  }
  return oldest
}

/** "<1m" / "41m" / "2h" / "3d": a compact age without "ago". */
export function formatCompactAge(iso: string, now: number | Date = Date.now()): string {
  const then = new Date(iso).getTime()
  const n = typeof now === 'number' ? now : now.getTime()
  if (Number.isNaN(then)) return '—'
  const mins = Math.floor((n - then) / 60000)
  if (mins < 1) return '<1m'
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

export const STALE_MS = 30 * 60 * 1000

/** True when an entry has waited longer than 30 minutes. */
export function isStale(iso: string, now: number = Date.now()): boolean {
  const then = new Date(iso).getTime()
  return !Number.isNaN(then) && now - then > STALE_MS
}

/** "m:ss", or "h:mm:ss" past an hour. */
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const ss = String(s).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

export type HomeStation = { key: string; name: string; color: string; count: number; isGate: boolean }

/** One station per workflow label (pipeline strip), flagged by the human-gate rule. */
export function labelStations(workflow: Workflow, counts: Record<string, number>): HomeStation[] {
  return [...workflow.labels]
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((l) => ({
      key: l.id,
      name: labelDisplayName(l),
      color: l.color,
      count: counts[l.name] ?? 0,
      isGate: isHumanGateLabel(workflow, l.name),
    }))
}

/**
 * Stations as the scene shows them: the default workflow has one per label;
 * custom workflows collapse to three buckets, of which "Waiting on human" is
 * the gate.
 */
export function homeStations(workflow: Workflow, counts: Record<string, number>): HomeStation[] {
  const idToName = new Map(workflow.labels.map((l) => [l.id, l.name]))
  const bucketed = bucketize(workflow) !== null
  return stationsFor(workflow, counts).map((s) => ({
    key: s.key,
    name: s.name,
    color: s.color,
    count: s.count,
    isGate: bucketed ? s.key === 'waitingHuman' : isHumanGateLabel(workflow, idToName.get(s.key) ?? ''),
  }))
}
