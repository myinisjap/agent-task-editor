import { CircleCheck, Hand } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, type AutonomyDecision, type WorkflowLabel } from '../../api/client'
import { buildBannerModel } from '../../lib/autonomyDecision'
import { labelDisplayNameByName } from '../../lib/labelDisplay'
import RiskFlagChips from '../shared/RiskFlagChips'
import { Icon } from '../ui'

// AutonomyDecisionBanner shows what the gate's autonomy dial would have done
// (shadow mode). Renders nothing without a decision or in `ask` mode.
export default function AutonomyDecisionBanner({
  taskId, refreshKey, labels, alwaysAsk, verification, compact = false,
}: {
  taskId: string
  refreshKey: string | number
  labels?: readonly WorkflowLabel[]
  alwaysAsk: boolean
  verification?: string | null
  compact?: boolean
}) {
  const [decision, setDecision] = useState<AutonomyDecision | null>(null)

  useEffect(() => {
    let cancelled = false
    Promise.resolve()
      .then(() => api.tasks.autonomyDecision(taskId))
      .then((d) => { if (!cancelled) setDecision(d ?? null) })
      .catch(() => { if (!cancelled) setDecision(null) })
    return () => { cancelled = true }
  }, [taskId, refreshKey])

  const model = buildBannerModel(decision, {
    alwaysAsk,
    verification,
    gateName: decision ? labelDisplayNameByName(decision.label, labels) : '',
  })
  if (!model) return null
  const approve = model.kind === 'approve'
  const box = compact
    ? 'border-t border-border pt-1.5'
    : approve
      ? 'rounded border border-success/40 bg-success-soft px-3 py-2'
      : 'rounded border border-border bg-surface-sunken px-3 py-2'

  return (
    <div data-testid="autonomy-banner" data-decision={model.kind} role="status" className={`${box} flex flex-col gap-1 min-w-0`}>
      <div className="flex items-center gap-1.5">
        <Icon icon={approve ? CircleCheck : Hand} size="sm" className={approve ? 'text-success' : 'text-fg-muted'} />
        <span className={`text-sm font-semibold ${approve ? 'text-success' : 'text-fg'}`}>
          {approve ? 'This would auto-approve' : 'The gate would still ask you'}
        </span>
      </div>
      <p className="text-xs text-fg break-words">{model.summary.join(' · ')}</p>
      {model.flagCodes.length > 0 && <RiskFlagChips codes={model.flagCodes} />}
      {model.stops.length > 0 && (
        <ul className="text-xs text-fg-muted list-disc pl-4">
          {model.stops.map((s) => <li key={s}>{s}</li>)}
        </ul>
      )}
      <p className="text-[11px] text-fg-subtle">Shadow mode: nothing is approved automatically yet.</p>
    </div>
  )
}
