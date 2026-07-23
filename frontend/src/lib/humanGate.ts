import type { Workflow, WorkflowLabel } from '../api/client'

/**
 * Returns true when `labelName` is a "human gate" within `workflow`: a label
 * a task can be sitting on where nothing but a human can move it forward.
 *
 * Rules:
 *  - A terminal label (`is_terminal`) is never a gate — the task is done, not
 *    stuck waiting on a person.
 *  - A label with `agent_ignore` set is always a gate: the dispatcher will
 *    never pick up a task sitting on it, regardless of what transitions exist.
 *  - Otherwise, the label is a gate only if it has at least one outgoing
 *    transition and every outgoing transition is `trigger_type === 'human'`
 *    (no `agent`/`both` edge the dispatcher could take on its own). A label
 *    with zero outgoing transitions is a dead end, not a human gate, and is
 *    excluded (nothing — human or agent — can move it forward; treating it as
 *    "needs human" would misrepresent a workflow-authoring issue).
 */
export function isHumanGateLabel(workflow: Workflow | undefined, labelName: string): boolean {
  if (!workflow) return false
  const label = workflow.labels.find((l) => l.name === labelName)
  if (!label) return false
  if (label.is_terminal) return false
  if (label.agent_ignore) return true

  const outgoing = workflow.transitions.filter((t) => t.from_label === labelName)
  if (outgoing.length === 0) return false

  return outgoing.every((t) => t.trigger_type === 'human')
}

/** Convenience: look up a label object by name within a workflow. */
export function findLabel(workflow: Workflow | undefined, labelName: string): WorkflowLabel | undefined {
  return workflow?.labels.find((l) => l.name === labelName)
}
