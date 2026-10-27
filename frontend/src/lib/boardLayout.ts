import type { Task } from '../api/client'

export type ColumnMode = 'full' | 'narrow' | 'collapsed'
export type ColumnInfo = { key: string; count: number; isTerminal: boolean; isStarting: boolean }

export const FULL_MIN_REM = 15
export const NARROW_PX = 40

/** Decide how each column renders. The starting column is always full (it hosts "+ Add task"). */
export function computeColumnModes(cols: ColumnInfo[], expandedTerminals: Set<string>): Map<string, ColumnMode> {
  const m = new Map<string, ColumnMode>()
  for (const c of cols) {
    let mode: ColumnMode
    if (c.isStarting) mode = 'full'
    else if (c.isTerminal) mode = expandedTerminals.has(c.key) ? 'full' : 'collapsed'
    else if (c.count === 0) mode = 'narrow'
    else mode = 'full'
    m.set(c.key, mode)
  }
  return m
}

export function columnWidthClass(mode: ColumnMode, expandedTransient: boolean): string {
  if (mode === 'full' || expandedTransient) return 'flex-[1_0_15rem] max-w-md'
  return 'w-10'
}

export function terminalStorageKey(workflowId: string): string {
  return 'board.expandedTerminals.' + workflowId
}

export function loadExpandedTerminals(workflowId: string | undefined): Set<string> {
  if (!workflowId) return new Set()
  try {
    const raw = localStorage.getItem(terminalStorageKey(workflowId))
    if (!raw) return new Set()
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return new Set()
    return new Set(parsed.filter((x): x is string => typeof x === 'string'))
  } catch {
    return new Set()
  }
}

export function saveExpandedTerminals(workflowId: string | undefined, set: Set<string>): void {
  if (!workflowId) return
  try {
    localStorage.setItem(terminalStorageKey(workflowId), JSON.stringify([...set]))
  } catch {
    // ignore
  }
}

export function groupTasksByRepo(
  tasks: Task[],
  repos: { id: string; name: string }[],
): { repoId: string; name: string; tasks: Task[] }[] {
  const known = new Map(repos.map((r) => [r.id, r.name]))
  const buckets = new Map<string, Task[]>()
  const unknown: Task[] = []
  for (const t of tasks) {
    if (t.repo_id && known.has(t.repo_id)) {
      const b = buckets.get(t.repo_id)
      if (b) b.push(t)
      else buckets.set(t.repo_id, [t])
    } else {
      unknown.push(t)
    }
  }
  const lanes = [...buckets.entries()]
    .map(([repoId, ts]) => ({ repoId, name: known.get(repoId) ?? '', tasks: ts }))
    .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()))
  if (unknown.length > 0) lanes.push({ repoId: '', name: 'Unknown repo', tasks: unknown })
  return lanes
}
