// Task dispatch priority levels. Mirrors the backend's plain-integer
// tasks.priority column (see backend/internal/api/handlers/tasks.go's
// PriorityLow/Normal/High/Urgent constants) so the two stay in lockstep.
// ListAgentPickupTasks orders eligible tasks by priority DESC, then
// created_at ASC — priority only affects dispatch *order*, never preempts an
// already-running task.
export const PRIORITY_LEVELS = [
  { value: -1, label: 'Low' },
  { value: 0, label: 'Normal' },
  { value: 1, label: 'High' },
  { value: 2, label: 'Urgent' },
] as const

export type PriorityValue = (typeof PRIORITY_LEVELS)[number]['value']

/** Human-readable label for a priority value. Defaults to "Normal" when n is undefined/unknown. */
export function priorityLabel(n?: number | null): string {
  const level = PRIORITY_LEVELS.find((l) => l.value === n)
  return level ? level.label : 'Normal'
}
