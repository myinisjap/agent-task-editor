import type { NeedsMeReason } from '../api/client'

/** Short human-readable label for why a task needs the user. */
export function needsMeReasonLabel(reason: NeedsMeReason): string {
  switch (reason) {
    case 'waiting_human':
      return 'Agent asked a question'
    case 'human_gate':
      return 'Waiting for your review'
    case 'pr_conflict':
      return 'PR has conflicts'
    default:
      return 'Needs you'
  }
}
