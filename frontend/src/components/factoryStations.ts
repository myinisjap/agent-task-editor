// Station layout for the "factory assembly line" dashboard visualization. Kept
// out of FactoryLine.tsx so that file only exports a component (Fast Refresh
// rule), mirroring taskBuckets.ts alongside TaskFactory.tsx.
import type { Workflow } from '../api/client'
import { bucketize } from './taskBuckets'
import { DEFAULT_FACTORY_ACTIONS, BUCKET_FACTORY_ACTIONS, type FactoryAction } from './factoryMachines'

export type FactoryStation = {
  key: string
  name: string
  color: string
  action: FactoryAction
  count: number
}

const sum = (names: string[], counts: Record<string, number>) => names.reduce((s, n) => s + (counts[n] ?? 0), 0)

// The seeded default workflow gets one bespoke machine per label; any custom
// workflow collapses to the same 3 buckets the office scene uses.
export function stationsFor(workflow: Workflow, labelCounts: Record<string, number>): FactoryStation[] {
  const buckets = bucketize(workflow)
  if (buckets) {
    return [
      { key: 'notReady', name: 'Not ready', color: '#94a3b8', action: BUCKET_FACTORY_ACTIONS.notReady, count: sum(buckets.notReady, labelCounts) },
      { key: 'agentWorking', name: 'Agent working', color: '#6366F1', action: BUCKET_FACTORY_ACTIONS.agentWorking, count: sum(buckets.agentWorking, labelCounts) },
      { key: 'waitingHuman', name: 'Waiting on human', color: '#EC4899', action: BUCKET_FACTORY_ACTIONS.waitingHuman, count: sum(buckets.waitingHuman, labelCounts) },
    ]
  }
  return [...workflow.labels]
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((label) => ({
      key: label.id,
      name: label.name,
      color: label.color,
      action: DEFAULT_FACTORY_ACTIONS[label.name] ?? 'idle',
      count: labelCounts[label.name] ?? 0,
    }))
}
