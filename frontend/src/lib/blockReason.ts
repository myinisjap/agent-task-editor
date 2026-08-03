import type { BlockReason } from '../api/client'

/** Short, badge-friendly label per BlockReason.code (see openapi.yaml's BlockReason schema). */
const CODE_LABELS: Record<string, string> = {
  paused: 'Paused',
  agent_ignore: 'Label excluded',
  dependency: 'Blocked on dependency',
  retry_backoff: 'Retrying',
  no_config: 'No agent config',
  repo_concurrency: 'Repo at capacity',
  rate_limited: 'Rate limited',
  cost_budget: 'Budget exhausted',
  wip_limit: 'WIP limit reached',
}

/** Reason codes where clears_at (if present) represents a natural, wait-it-out expiry. */
const TRANSIENT_CODES = new Set(['rate_limited', 'retry_backoff'])

export function blockReasonLabel(reason: BlockReason): string {
  return CODE_LABELS[reason.code] ?? reason.code
}

/** Whether reason.clears_at (if set) should be rendered as a countdown — i.e. "wait" is the correct action. */
export function isTransientBlockReason(reason: BlockReason): boolean {
  return TRANSIENT_CODES.has(reason.code)
}
