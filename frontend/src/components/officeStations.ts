// Station derivation for the Office/Robots scene (TaskFactory.tsx). Kept out of
// the component file so TaskFactory only exports a component (Fast Refresh) and
// so this is unit-testable. NOT factoryStations.ts (that belongs to FactoryLine).
import type { Task, Workflow } from '../api/client'
import { isHumanGateLabel } from '../lib/humanGate'
import { blockReasonLabel } from '../lib/blockReason'
import { bucketize } from './taskBuckets'
import { DEFAULT_ACTIONS, BUCKET_ACTIONS } from './pixelSprites'
import { isBusy, shippedBoxes, type Station, type SceneTask } from './officeScene'

export type { SceneTask, SceneTaskType } from './officeScene'
export { isBusy, shippedBoxes }

const sum = (names: string[], counts: Record<string, number>) => names.reduce((s, n) => s + (counts[n] ?? 0), 0)

export type StationOpts = { rateLimitedUntil?: Record<string, string>; now?: number }

const rank = (t: SceneTask) => (isBusy(t) ? 0 : t.rateLimited ? 1 : 2)

function toSceneTask(t: Task, order: number, opts: StationOpts): SceneTask {
  const now = opts.now ?? Date.now()
  const live = (iso: string | null | undefined) => !iso || !(Date.parse(iso) <= now)
  const br = t.block_reason
  const wsUntil = opts.rateLimitedUntil?.[t.id]
  const fromBlock = br?.code === 'rate_limited' && live(br.clears_at)
  const fromWs = wsUntil !== undefined && live(wsUntil)
  return {
    id: t.id,
    title: t.title,
    type: t.type,
    order,
    runStatus: t.active_run_status,
    blockCode: br?.code,
    clearsAt: fromBlock ? (br?.clears_at ?? null) : fromWs ? wsUntil || null : (br?.clears_at ?? null),
    rateLimited: fromBlock || fromWs,
    queuePos: t.queue_position ?? null,
    priority: t.priority ?? 0,
  }
}

function cmpTasks(a: SceneTask, b: SceneTask): number {
  const r = rank(a) - rank(b)
  if (r) return r
  const qa = a.queuePos ?? Infinity
  const qb = b.queuePos ?? Infinity
  if (qa !== qb) return qa < qb ? -1 : 1
  const p = (b.priority ?? 0) - (a.priority ?? 0)
  if (p) return p
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

/**
 * Stations for the scene. With `tasks`, every station also carries the tasks it
 * represents (task mode); without, it's the count-only fallback.
 */
export function officeStationsFor(
  workflow: Workflow, labelCounts: Record<string, number>, tasks?: Task[], opts: StationOpts = {},
): Station[] {
  const stations = baseStations(workflow, labelCounts)
  if (!tasks) return stations
  const buckets = bucketize(workflow)
  const orderOf = new Map(workflow.labels.map((l) => [l.name, l.sort_order]))
  const keyOf = (label: string): string | undefined => {
    if (!buckets) return workflow.labels.find((l) => l.name === label)?.id
    for (const k of ['notReady', 'agentWorking', 'waitingHuman'] as const) if (buckets[k].includes(label)) return k
    return undefined
  }
  const by = new Map<string, SceneTask[]>(stations.map((s) => [s.key, []]))
  for (const t of tasks) {
    if (t.workflow_id !== workflow.id || t.archived) continue
    const key = keyOf(t.label)
    const list = key ? by.get(key) : undefined
    if (list) list.push(toSceneTask(t, orderOf.get(t.label) ?? 0, opts))
  }
  const terminal = new Set(buckets ? [] : workflow.labels.filter((l) => l.is_terminal === 1).map((l) => l.id))
  return stations.map((s) => ({ ...s, tasks: by.get(s.key)!.sort(cmpTasks), terminal: terminal.has(s.key) }))
}

/** Tooltip status line for a scene task. */
export function sceneTaskStatus(
  t: SceneTask, gate: boolean,
  fmtTime: (iso: string) => string = (iso) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
): string {
  if (t.rateLimited) return t.clearsAt ? `rate limited until ${fmtTime(t.clearsAt)}` : 'rate limited'
  if (t.runStatus === 'running') return 'running'
  if (t.runStatus === 'pending') return 'starting'
  if (t.runStatus === 'waiting_human' || gate) return 'waiting on you'
  if (t.blockCode) return blockReasonLabel({ code: t.blockCode, message: '' } as Parameters<typeof blockReasonLabel>[0]).toLowerCase()
  return 'queued'
}

function baseStations(workflow: Workflow, labelCounts: Record<string, number>): Station[] {
  const buckets = bucketize(workflow)
  if (buckets) {
    return [
      { key: 'notReady', name: 'Not ready', color: '#94a3b8', action: BUCKET_ACTIONS.notReady, count: sum(buckets.notReady, labelCounts) },
      { key: 'agentWorking', name: 'Agent working', color: '#6366F1', action: BUCKET_ACTIONS.agentWorking, count: sum(buckets.agentWorking, labelCounts) },
      // The "waiting on human" bucket is a human gate by definition.
      { key: 'waitingHuman', name: 'Waiting on human', color: '#EC4899', action: BUCKET_ACTIONS.waitingHuman, count: sum(buckets.waitingHuman, labelCounts), gate: true },
    ]
  }
  return [...workflow.labels]
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((label) => ({
      key: label.id,
      name: label.name,
      color: label.color,
      action: DEFAULT_ACTIONS[label.name] ?? 'idle',
      count: labelCounts[label.name] ?? 0,
      // There is no `is_human_gate` field; isHumanGateLabel is the equivalent.
      // It also returns true for agent_ignore labels (backlog / not_ready), but
      // a parked backlog is not "waiting on a human" (matches /needs-me, which
      // excludes backlog), so those are excluded here.
      gate: isHumanGateLabel(workflow, label.name) && !label.agent_ignore,
    }))
}
