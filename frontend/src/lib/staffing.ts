import type { AgentConfig, Workflow, WorkflowLabel } from '../api/client'
import { agentTriggerableLabels, parseAgentLabels } from './setupStatus'

/** Who works a workflow label, as shown on the Pipeline graph and Board headers. */
export type LabelStaffing = {
  /**
   * agent     - agent-triggerable and at least one enabled agent covers it
   * unstaffed - agent-triggerable but nobody (enabled) covers it
   * terminal  - the task is done here; nobody works it
   * human     - not agent-triggerable: only a human moves tasks out of it
   */
  kind: 'agent' | 'unstaffed' | 'terminal' | 'human'
  /** Agent configs assigned to this label (by labels list or an explicit transition), enabled or not. */
  agents: AgentConfig[]
}

/** Agent configs whose `labels` include `labelName`, or that a transition out of it names explicitly. */
export function agentsForLabel(
  workflow: Workflow,
  agents: AgentConfig[],
  labelName: string,
): AgentConfig[] {
  const explicit = new Set(
    (workflow.transitions ?? [])
      .filter((t) => t.from_label === labelName && t.agent_config_id)
      .map((t) => t.agent_config_id as string),
  )
  return agents.filter((a) => parseAgentLabels(a.labels).includes(labelName) || explicit.has(a.id))
}

export function labelStaffing(
  workflow: Workflow,
  agents: AgentConfig[],
  label: WorkflowLabel,
): LabelStaffing {
  const assigned = agentsForLabel(workflow, agents, label.name)
  if (label.is_terminal) return { kind: 'terminal', agents: assigned }
  if (!agentTriggerableLabels(workflow).includes(label.name)) {
    return { kind: 'human', agents: assigned }
  }
  return { kind: assigned.some((a) => a.enabled) ? 'agent' : 'unstaffed', agents: assigned }
}

/** Staffing for every label of a workflow, keyed by label name. */
export function workflowStaffing(
  workflow: Workflow,
  agents: AgentConfig[],
): Map<string, LabelStaffing> {
  return new Map((workflow.labels ?? []).map((l) => [l.name, labelStaffing(workflow, agents, l)]))
}

/** Names of agent-triggerable labels in this workflow that no enabled agent covers. */
export function unstaffedLabelNames(workflow: Workflow | undefined, agents: AgentConfig[]): Set<string> {
  const out = new Set<string>()
  if (!workflow) return out
  for (const [name, s] of workflowStaffing(workflow, agents)) if (s.kind === 'unstaffed') out.add(name)
  return out
}

/** Returns the labels JSON for `agent` with `labelName` added or removed (stable order, no dupes). */
export function toggleAgentLabel(agent: AgentConfig, labelName: string, assigned: boolean): string {
  const current = parseAgentLabels(agent.labels).filter((l) => l !== labelName)
  return JSON.stringify(assigned ? [...current, labelName] : current)
}
