// Station derivation for the Office/Robots scene (TaskFactory.tsx). Kept out of
// the component file so TaskFactory only exports a component (Fast Refresh) and
// so this is unit-testable. NOT factoryStations.ts (that belongs to FactoryLine).
import type { Workflow } from '../api/client'
import { isHumanGateLabel } from '../lib/humanGate'
import { bucketize } from './taskBuckets'
import { DEFAULT_ACTIONS, BUCKET_ACTIONS } from './pixelSprites'
import type { Station } from './officeScene'

const sum = (names: string[], counts: Record<string, number>) => names.reduce((s, n) => s + (counts[n] ?? 0), 0)

export function officeStationsFor(workflow: Workflow, labelCounts: Record<string, number>): Station[] {
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
