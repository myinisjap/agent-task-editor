import type { AutonomyDecision } from '../api/client'

export const MODE_NAME: Record<AutonomyDecision['mode'], string> = {
  ask: 'Ask',
  on_flags: 'Ask on flags',
  auto: 'Auto',
}

export type BannerModel = {
  kind: 'approve' | 'ask'
  summary: string[]
  flagCodes: string[]
  stops: string[]
} | null

function verifyStop(detail: string): string {
  const status = detail.replace('verification status: ', '').trim()
  if (status === 'fail') return 'Verification failed'
  if (status === 'timeout') return 'Verification timed out'
  if (status === 'error') return 'Verification errored'
  return 'Verification did not pass'
}

// buildBannerModel turns a recorded shadow decision into banner copy.
// The PATCH /always-ask endpoint does not re-record a decision, so the live
// `alwaysAsk` flag overrides the recorded one: always_ask => ask (the
// evaluator guarantees it). Turning it off never flips to approve, since the
// other inputs are unknown.
export function buildBannerModel(
  decision: AutonomyDecision | null,
  opts: { alwaysAsk: boolean; verification?: string | null; gateName: string },
): BannerModel {
  if (!decision || decision.mode === 'ask' || decision.reasons.some((r) => r.code === 'mode_ask')) return null
  const summary = [`${opts.gateName} gate set to ${MODE_NAME[decision.mode]}`]
  const flagCodes: string[] = []
  const stops: string[] = []
  let kind: 'approve' | 'ask' = decision.decision
  let droppedAlwaysAsk = false

  for (const r of decision.reasons) {
    switch (r.code) {
      case 'always_ask':
        if (opts.alwaysAsk) stops.push('You marked this task Always ask')
        else droppedAlwaysAsk = true
        break
      case 'verify_failed': stops.push(verifyStop(r.detail)); break
      case 'cost_exceeded': stops.push(`Cost over the cap (${r.detail})`); break
      case 'no_diff': stops.push('No diff to check for flags'); break
      case 'flag_raised': flagCodes.push(r.detail); break
      case 'no_blocking_flags': if (kind === 'approve') summary.push('no flags'); break
      case 'auto_policy': if (kind === 'approve') summary.push('no hard stops'); break
      default: if (kind === 'ask') stops.push(r.detail)
    }
  }
  if (opts.alwaysAsk && kind === 'approve') {
    kind = 'ask'
    summary.length = 1
    stops.push('You marked this task Always ask')
  }
  if (kind === 'approve' && opts.verification === 'pass') summary.push('verify passed')
  if (kind === 'ask' && droppedAlwaysAsk && stops.length === 0 && flagCodes.length === 0) {
    summary.push('Always ask was turned off; the gate decides again on its next landing')
  }
  return { kind, summary, flagCodes, stops }
}
