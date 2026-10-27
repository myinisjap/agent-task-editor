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
  /** One line: what happened. */
  explanation: string
  /** One line: what to do about it. */
  nextStep: string
}

type Wording = { explanation: string; nextStep: string }

const WORDING: Record<EscalationReason, Wording> = {
  agent_question: { explanation: 'The agent paused because it needs an answer from you.', nextStep: 'Reply below; it continues in the same session.' },
  auth_required: { explanation: 'The provider CLI has no valid login, so the run was refused.', nextStep: 'Log the CLI in (see Health), then retry.' },
  repeated_failure: { explanation: 'The run failed several times in a row, so automatic retries stopped.', nextStep: 'Check the Logs tab for the error, then retry or reply with guidance.' },
  rework_loop: { explanation: 'The task bounced between review and implementation too many times.', nextStep: "Reply with guidance on what's blocking it, or move the task yourself." },
  cost_budget: { explanation: "The run stopped because it reached the agent's cost budget.", nextStep: 'Raise the budget on the agent, then retry.' },
  cost_unknown: { explanation: 'The agent has a cost budget but there is no pricing for its model.', nextStep: 'Add pricing for the model, then retry.' },
  provider_missing: { explanation: "The agent's provider is disabled or no longer exists.", nextStep: 'Enable or fix the provider, then retry.' },
  runtime_prep: { explanation: "Installing the repo's toolchain failed before the agent started.", nextStep: "Check the repo's runtime settings, then retry." },
  verify_error: { explanation: "The repo's verify command couldn't start (this is not a test failure).", nextStep: 'Fix the verify command on the repo, then retry.' },
  max_turns: { explanation: 'The agent used all its allowed turns before finishing.', nextStep: 'Raise max turns or split the task, then retry.' },
  transient_exhausted: { explanation: 'Network or provider errors kept coming back and retries ran out.', nextStep: 'Retry once the provider is healthy.' },
  no_outcome: { explanation: 'The agent exited without reporting success or failure.', nextStep: 'Check the Logs tab, then retry.' },
  rejected_transition: { explanation: "The agent asked for a move the workflow doesn't allow.", nextStep: 'Retry, or move the task yourself.' },
}

const FALLBACK_WORDING: Wording = { explanation: 'The agent paused and needs you.', nextStep: 'Open the task and reply.' }

const BASE: Record<EscalationReason, Omit<EscalationInfo, 'explanation' | 'nextStep'>> = {
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
  verify_error: {
    short: 'Verify failed to run',
    label: 'Verify command could not be run',
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

const INFO = Object.fromEntries(
  (Object.keys(BASE) as EscalationReason[]).map((r) => [r, { ...BASE[r], ...WORDING[r] }]),
) as Record<EscalationReason, EscalationInfo>

/** Every known reason, for tests and exhaustive UIs. */
export const ESCALATION_REASONS = Object.keys(INFO) as EscalationReason[]

/**
 * Describes an escalation reason. Unknown or missing codes (runs recorded
 * before the field existed, or a newer server) fall back to the legacy
 * behaviour: treat it as a question the user can reply to.
 */
export function escalationInfo(reason: string | null | undefined): EscalationInfo {
  if (reason && reason in INFO) return INFO[reason as EscalationReason]
  return { short: 'Needs you', label: 'Agent is waiting for your input', action: 'reply', ...FALLBACK_WORDING }
}
