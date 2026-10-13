import type { WorkflowTransition } from '../api/client'

/**
 * Whether a human may move a task from `fromLabel` to `toLabel`.
 *
 * Mirrors the backend rule (workflow.Engine.Transition with TriggerHuman,
 * used by PATCH /tasks/{id}/label): a human move is accepted iff a transition
 * edge exists from->to in the workflow, whatever its trigger_type. The only
 * trigger_type restriction ("human"-only edges can't be taken by an agent)
 * applies to agent-triggered moves, not human ones. Moving to the same label
 * is a no-op, so it's not a legal move.
 *
 * An empty transition list means the workflow hasn't loaded (or isn't known),
 * so nothing is ruled out client-side and the server stays the authority.
 */
export function canHumanMove(
  transitions: readonly WorkflowTransition[],
  fromLabel: string,
  toLabel: string,
): boolean {
  if (fromLabel === toLabel) return false
  if (transitions.length === 0) return true
  return transitions.some((t) => t.from_label === fromLabel && t.to_label === toLabel)
}
