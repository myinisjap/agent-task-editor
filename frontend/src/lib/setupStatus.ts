import type { AgentConfig, Workflow } from '../api/client'
import { TEMPLATES } from './agentTemplates'

// Shared definition of "setup" used by the Get Started checklist and the SPA
// index route (HomeRoute). Pure helpers live here; the store-backed hook is in
// useSetupStatus.ts.

// DISMISSED_STORAGE_KEY follows the CONDENSED_STORAGE_KEY naming convention
// used elsewhere on the board (see BoardPage.tsx).
export const ONBOARDING_DISMISSED_KEY = 'board.onboarding.dismissed'

export function readOnboardingDismissed(): boolean {
  try {
    return localStorage.getItem(ONBOARDING_DISMISSED_KEY) === 'true'
  } catch {
    return false
  }
}

/** Parse an agent config's `labels` JSON string; tolerant of bad data. */
export function parseAgentLabels(raw: string | undefined | null): string[] {
  try {
    const v = JSON.parse(raw || '[]')
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

/**
 * Labels in a workflow that an agent works: the source of at least one
 * agent/both transition, and not marked agent_ignore. This mirrors the
 * dispatcher's pickup rule (see storage/queries/tasks.sql).
 */
export function agentTriggerableLabels(workflow: Workflow): string[] {
  const ignored = new Set(
    (workflow.labels ?? []).filter((l) => l.agent_ignore).map((l) => l.name),
  )
  const from = new Set(
    (workflow.transitions ?? [])
      .filter((t) => t.trigger_type === 'agent' || t.trigger_type === 'both')
      .map((t) => t.from_label),
  )
  return (workflow.labels ?? [])
    .filter((l) => from.has(l.name) && !ignored.has(l.name))
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((l) => l.name)
}

/** Agent-triggerable labels (across all workflows) with no enabled agent config. */
export function uncoveredAgentLabels(workflows: Workflow[], agents: AgentConfig[]): string[] {
  const covered = new Set<string>()
  for (const a of agents) {
    if (!a.enabled) continue
    for (const l of parseAgentLabels(a.labels)) covered.add(l)
  }
  const missing: string[] = []
  for (const wf of workflows) {
    // An explicit per-transition agent assignment also covers its source label.
    const assigned = new Set(
      (wf.transitions ?? []).filter((t) => t.agent_config_id).map((t) => t.from_label),
    )
    for (const l of agentTriggerableLabels(wf)) {
      if (!covered.has(l) && !assigned.has(l) && !missing.includes(l)) missing.push(l)
    }
  }
  return missing
}

export type StaffingPlan = {
  /** Templates to create, in label order, one per uncovered label. */
  templates: typeof TEMPLATES
  /** Uncovered labels no built-in template handles (must be staffed by hand). */
  unmatched: string[]
}

/** Map uncovered labels to the built-in agent templates that serve them. */
export function planStaffing(missingLabels: string[]): StaffingPlan {
  const templates: typeof TEMPLATES = []
  const unmatched: string[] = []
  for (const label of missingLabels) {
    const t = TEMPLATES.find((tpl) => parseAgentLabels(tpl.labels).includes(label))
    if (!t) unmatched.push(label)
    else if (!templates.includes(t)) templates.push(t)
  }
  return { templates, unmatched }
}

export type SetupInputs = {
  repoCount: number
  providerCount: number
  taskCount: number
  workflows: Workflow[]
  agents: AgentConfig[]
}

export type SetupState = {
  hasRepo: boolean
  hasProvider: boolean
  missingAgentLabels: string[]
  staffed: boolean
  hasTask: boolean
  complete: boolean
}

export function computeSetup(i: SetupInputs): SetupState {
  const missingAgentLabels = uncoveredAgentLabels(i.workflows, i.agents)
  const hasRepo = i.repoCount > 0
  const hasProvider = i.providerCount > 0
  const staffed = missingAgentLabels.length === 0
  const hasTask = i.taskCount > 0
  return {
    hasRepo,
    hasProvider,
    missingAgentLabels,
    staffed,
    hasTask,
    complete: hasRepo && hasProvider && staffed && hasTask,
  }
}
