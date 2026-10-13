import type { NeedsMeReason } from '../api/client'
import { escalationInfo } from './escalation'

/**
 * Short human-readable label for why a task needs the user. For a waiting run
 * the escalation code (when recorded) names the real cause instead of
 * assuming the agent asked a question.
 */
export function needsMeReasonLabel(reason: NeedsMeReason, escalationReason?: string | null): string {
  switch (reason) {
    case 'waiting_human':
      return escalationReason ? escalationInfo(escalationReason).label : 'Agent asked a question'
    case 'human_gate':
      return 'Waiting for your review'
    case 'pr_conflict':
      return 'PR has conflicts'
    default:
      return 'Needs you'
  }
}
