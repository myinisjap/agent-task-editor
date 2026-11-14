import type { Workflow } from '../api/client'

// The server decides which workflow is the default (is_default on
// GET /workflows); this only reads the flag. list[0] is a safety net.
export function defaultWorkflow(list: Workflow[]): Workflow | undefined {
  return list.find((w) => w.is_default) ?? list[0]
}

export function resolveSelectedWorkflow(list: Workflow[], selectedId: string | null | undefined): Workflow | undefined {
  return list.find((w) => w.id === selectedId) ?? defaultWorkflow(list)
}
