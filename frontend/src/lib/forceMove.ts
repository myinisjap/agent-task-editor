import type { WorkflowLabel } from '../api/client'

// forceMoveTargets lists the labels a forced move can target: every label of
// the workflow except the task's current one, in board order.
export function forceMoveTargets(labels: WorkflowLabel[], current: string): WorkflowLabel[] {
  return [...labels].sort((a, b) => a.sort_order - b.sort_order).filter((l) => l.name !== current)
}
