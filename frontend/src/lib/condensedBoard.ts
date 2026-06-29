import type { WorkflowLabel, WorkflowTransition } from '../api/client'

export type CondensedGroup =
  | { kind: 'single'; label: WorkflowLabel }
  | { kind: 'agent-group'; labels: WorkflowLabel[] }

/**
 * Computes how to collapse the board columns into a condensed view.
 *
 * Rules:
 *  - Labels that have ANY outgoing transition with trigger_type 'human' or 'both'
 *    are "human-touched" and always get their own column.
 *  - Labels with agent_ignore=1 or is_terminal=1 always get their own column.
 *  - Labels with NO outgoing transitions (sink labels) always get their own column.
 *  - Consecutive pure-agent labels (all outgoing transitions are agent-only) are
 *    collapsed into a single { kind: 'agent-group' } entry.
 *  - A single-label agent group is still returned as 'agent-group' so the caller
 *    can consistently render a label badge on the task cards.
 */
export function computeCondensedGroups(
  labels: WorkflowLabel[],
  transitions: WorkflowTransition[],
): CondensedGroup[] {
  // Build set of label names that have ≥1 human (or both) outgoing transition
  const humanTouchedLabels = new Set<string>()
  for (const t of transitions) {
    if (t.trigger_type === 'human' || t.trigger_type === 'both') {
      humanTouchedLabels.add(t.from_label)
    }
  }

  // Build set of label names that have any outgoing transition at all
  const hasOutgoing = new Set<string>()
  for (const t of transitions) {
    hasOutgoing.add(t.from_label)
  }

  const sorted = [...labels].sort((a, b) => a.sort_order - b.sort_order)

  const groups: CondensedGroup[] = []
  let agentBuffer: WorkflowLabel[] = []

  const flushAgentBuffer = () => {
    if (agentBuffer.length === 0) return
    groups.push({ kind: 'agent-group', labels: [...agentBuffer] })
    agentBuffer = []
  }

  for (const label of sorted) {
    const isPureAgent =
      !humanTouchedLabels.has(label.name) &&
      !label.agent_ignore &&
      !label.is_terminal &&
      hasOutgoing.has(label.name) // has at least one outgoing transition

    if (isPureAgent) {
      agentBuffer.push(label)
    } else {
      flushAgentBuffer()
      groups.push({ kind: 'single', label })
    }
  }

  flushAgentBuffer()

  return groups
}
