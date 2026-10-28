import { Link } from 'react-router-dom'
import type { PausedProvider } from '../../api/client'
import { Badge } from '../ui'

type Props = {
  paused: PausedProvider[]
  /** Set when the global spend cap has tripped (also halts dispatch). */
  budgetTripped?: boolean
  budgetReason?: string
}

/** Green "Dispatch running", or red "Dispatch paused · <reason>". */
export default function DispatchPill({ paused, budgetTripped, budgetReason }: Props) {
  if (paused.length === 0 && !budgetTripped) {
    return <Badge tone="success" data-testid="dispatch-pill">Dispatch running</Badge>
  }
  const reasons = paused.map((p) => `${p.provider}: ${p.reason}`)
  if (budgetTripped) reasons.push(`${budgetReason ?? 'spend'} spend cap reached`)
  const first = paused.length > 0 ? paused[0].reason : `${budgetReason ?? 'daily'} spend cap`
  const extra = reasons.length - 1
  return (
    <Link to="/settings/health" title={reasons.join('\n')} className="min-w-0">
      <Badge tone="danger" data-testid="dispatch-pill" className="max-w-full">
        <span className="truncate">Dispatch paused · {first}{extra > 0 ? ` +${extra}` : ''}</span>
      </Badge>
    </Link>
  )
}
