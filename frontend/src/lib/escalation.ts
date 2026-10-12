import type { components } from '../api/types'

/** Why a run went to waiting_human (agent_runs.escalation_reason). */
export type EscalationReason = NonNullable<components['schemas']['AgentRun']['escalation_reason']>

/** What the banner/queue should offer first for an escalated run. */
export type EscalationAction = 'reply' | 'retry' | 'fix'

export type EscalationInfo = {
  /** Short badge text (board card). */
  short: string
  /** Sentence-length heading for the task banner / queue. */
  label: string
  /** Primary action: reply (answer the agent), retry (re-dispatch as is), fix (change config first, then retry). */
  action: EscalationAction
  /** Where 'fix' sends the user. */
  fix?: { to: string; text: string }
}

const INFO: Record<EscalationReason, EscalationInfo> = {
  agent_question: { short: 'Question', label: 'Agent asked a question', action: 'reply' },
  auth_required: {
    short: 'Not logged in',
    label: 'Agent CLI is not logged in',
    action: 'fix',
    fix: { to: '/settings/health', text: 'Open Health' },
  },
  repeated_failure: { short: 'Failed repeatedly', label: 'Agent failed repeatedly', action: 'retry' },
  rework_loop: { short: 'Rework loop', label: 'Stuck in a rework loop', action: 'reply' },
  cost_budget: {
    short: 'Budget reached',
    label: 'Cost budget reached',
    action: 'fix',
    fix: { to: '/pipeline/agents', text: 'Raise budget' },
  },
  cost_unknown: {
    short: 'Cost unknown',
    label: 'Cost budget cannot be enforced',
    action: 'fix',
    fix: { to: '/settings/pricing', text: 'Add pricing' },
  },
  provider_missing: {
    short: 'Provider missing',
    label: 'Agent provider is disabled or unknown',
    action: 'fix',
    fix: { to: '/settings/providers', text: 'Open providers' },
  },
  runtime_prep: {
    short: 'Toolchain failed',
    label: 'Toolchain setup failed',
    action: 'fix',
    fix: { to: '/repos', text: 'Open repos' },
  },
  max_turns: {
    short: 'Turn limit',
    label: 'Agent hit its turn limit',
    action: 'fix',
    fix: { to: '/pipeline/agents', text: 'Raise max turns' },
  },
  transient_exhausted: { short: 'Retries exhausted', label: 'Retries exhausted after transient errors', action: 'retry' },
  no_outcome: { short: 'No outcome', label: 'Agent finished without an outcome', action: 'retry' },
  rejected_transition: { short: 'Move rejected', label: "Agent's requested move was rejected", action: 'retry' },
}

/**
 * Describes an escalation reason. Unknown or missing codes (runs recorded
 * before the field existed, or a newer server) fall back to the legacy
 * behaviour: treat it as a question the user can reply to.
 */
export function escalationInfo(reason: string | null | undefined): EscalationInfo {
  if (reason && reason in INFO) return INFO[reason as EscalationReason]
  return { short: 'Needs you', label: 'Agent is waiting for your input', action: 'reply' }
}
