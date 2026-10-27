import type { WorkflowLabel } from '../api/client'

// humanizeLabelName turns a raw label identifier into readable copy:
// separators become spaces, sentence case. `not_ready` -> "Not ready".
export function humanizeLabelName(name: string): string {
  const s = name.replace(/[-_]+/g, ' ').replace(/\s+/g, ' ').trim().toLowerCase()
  return s.charAt(0).toUpperCase() + s.slice(1)
}

// labelDisplayName is the visible title for a label: its configured
// display_name, else the humanized name. The raw `name` stays the identifier
// for API calls, URLs, ids and option values.
export function labelDisplayName(label: Pick<WorkflowLabel, 'name'> & { display_name?: string | null }): string {
  return label.display_name?.trim() || humanizeLabelName(label.name)
}

// labelDisplayNameByName resolves a raw label string (task.label, history
// entries) against a workflow's labels, falling back to the humanized name.
export function labelDisplayNameByName(name: string, labels?: readonly WorkflowLabel[] | null): string {
  const found = labels?.find((l) => l.name === name)
  return found ? labelDisplayName(found) : humanizeLabelName(name)
}
