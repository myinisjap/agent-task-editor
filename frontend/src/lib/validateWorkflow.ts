import type { ParsedWorkflow } from './parseWorkflowYaml'

export type WorkflowValidationError = {
  /** The label name this error pertains to, if applicable. */
  label?: string
  message: string
}

/**
 * Validate a parsed workflow against two invariants the workflow editor
 * enforces before saving:
 *
 *   1. Every label must be reachable from the start label (the label with
 *      the lowest `sortOrder` — this matches the seeding convention in
 *      backend/internal/storage/seed.go, where `not_ready` has
 *      `sort_order: 0` and is treated as the implicit start).
 *   2. Every label with `isTerminal: true` must have zero outgoing
 *      transitions.
 *
 * Returns an empty array when the workflow is valid.
 */
export function validateWorkflow(wf: ParsedWorkflow): WorkflowValidationError[] {
  const errors: WorkflowValidationError[] = []

  if (wf.labels.length === 0) {
    return [{ message: 'Workflow must have at least one label.' }]
  }

  // Determine the start label: the label with the minimum sortOrder. If
  // there's a tie, the first one encountered (in the YAML's label order)
  // wins — this is an inherent ambiguity in "lowest sort_order" when
  // sort_order isn't unique; we don't attempt to be clever about it.
  let start = wf.labels[0]
  for (const l of wf.labels) {
    if (l.sortOrder < start.sortOrder) start = l
  }

  const labelNames = new Set(wf.labels.map((l) => l.name))

  // Build adjacency list from -> [to, ...]. Also collect transitions that
  // reference unknown label names as a separate error, rather than silently
  // dropping them.
  const adjacency = new Map<string, string[]>()
  const unknownRefs = new Set<string>()

  for (const t of wf.transitions) {
    const fromKnown = labelNames.has(t.from)
    const toKnown = labelNames.has(t.to)
    if (!fromKnown) unknownRefs.add(t.from)
    if (!toKnown) unknownRefs.add(t.to)
    if (fromKnown && toKnown) {
      const list = adjacency.get(t.from) ?? []
      list.push(t.to)
      adjacency.set(t.from, list)
    }
  }

  for (const ref of unknownRefs) {
    errors.push({ message: `Transition references unknown label "${ref}".` })
  }

  // BFS from the start label to find all reachable labels.
  const reachable = new Set<string>([start.name])
  const queue: string[] = [start.name]
  while (queue.length > 0) {
    const current = queue.shift() as string
    const neighbors = adjacency.get(current) ?? []
    for (const n of neighbors) {
      if (!reachable.has(n)) {
        reachable.add(n)
        queue.push(n)
      }
    }
  }

  // Note: a label can independently be BOTH unreachable AND (if terminal)
  // have outgoing transitions — both checks run unconditionally and both
  // errors are reported when applicable, they are not mutually exclusive.
  //
  // Note: if the YAML has duplicate label names, later entries overwrite
  // earlier ones in `labelNames`/`reachable` bookkeeping only in the sense
  // that we iterate `wf.labels` as given; the first occurrence determines
  // position in `wf.labels[0]` fallback above. We don't attempt to dedupe
  // labels here — this is a simplifying assumption for an edge case that
  // shouldn't occur in well-formed workflow YAML.
  for (const label of wf.labels) {
    if (label.name === start.name) continue // trivially reachable
    if (!reachable.has(label.name)) {
      errors.push({
        label: label.name,
        message: `Label "${label.name}" is not reachable from the start label "${start.name}".`,
      })
    }
  }

  for (const label of wf.labels) {
    if (!label.isTerminal) continue
    const targets = adjacency.get(label.name) ?? []
    if (targets.length > 0) {
      const uniqueTargets = Array.from(new Set(targets))
      errors.push({
        label: label.name,
        message: `Terminal label "${label.name}" has outgoing transition(s) to ${uniqueTargets.map((t) => `"${t}"`).join(', ')}.`,
      })
    }
  }

  return errors
}
