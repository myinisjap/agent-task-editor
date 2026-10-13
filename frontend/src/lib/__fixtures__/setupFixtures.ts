import type { AgentConfig, Workflow } from '../../api/client'

const LABELS: [string, number, boolean, boolean][] = [
  ['not_ready', 0, true, false],
  ['plan', 1, false, false],
  ['review-plan', 2, false, false],
  ['work', 3, false, false],
  ['testing', 4, false, false],
  ['agent-review', 5, false, false],
  ['review', 6, false, false],
  ['done', 7, false, true],
]

const TRANSITIONS: [string, string, string][] = [
  ['agent-review', 'review', 'agent'],
  ['agent-review', 'work', 'agent'],
  ['not_ready', 'plan', 'human'],
  ['plan', 'review-plan', 'agent'],
  ['review', 'done', 'human'],
  ['review', 'work', 'human'],
  ['review-plan', 'plan', 'human'],
  ['review-plan', 'work', 'human'],
  ['testing', 'agent-review', 'agent'],
  ['testing', 'work', 'agent'],
  ['work', 'testing', 'agent'],
]

/** Mirrors the workflow seeded by backend/internal/storage/seed.go. */
export function defaultWorkflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf1',
    name: 'Default',
    description: '',
    labels: LABELS.map(([name, sort_order, ignore, terminal]) => ({
      id: name,
      workflow_id: 'wf1',
      name,
      color: '#000000',
      sort_order,
      agent_ignore: ignore ? 1 : 0,
      is_terminal: terminal ? 1 : 0,
      create_pr: 0,
      wip_limit_hard: 0,
    })),
    transitions: TRANSITIONS.map(([from_label, to_label, trigger_type], i) => ({
      id: `t${i}`,
      workflow_id: 'wf1',
      from_label,
      to_label,
      trigger_type,
    })),
    ...overrides,
  } as Workflow
}

/** One enabled agent config per label. */
export function agentFor(labels: string[], enabled = true): AgentConfig[] {
  return labels.map(
    (l) =>
      ({
        id: `a-${l}`,
        name: `agent-${l}`,
        provider_config_id: 'pc1',
        enabled,
        labels: JSON.stringify([l]),
        system_prompt: '',
      }) as AgentConfig,
  )
}
