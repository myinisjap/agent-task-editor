import { useEffect, useState } from 'react'
import { api, type AutonomyDecision, type NeedsMeItem, type WorkflowLabel } from '../../api/client'
import { buildBannerModel } from '../../lib/autonomyDecision'
import { labelDisplayNameByName } from '../../lib/labelDisplay'

/** "Why it qualified": the recorded autonomy decision's summary, with the row's chips as a fallback. */
export default function AutonomyReasons({ item, labels }: { item: NeedsMeItem; labels?: readonly WorkflowLabel[] }) {
  const [decision, setDecision] = useState<AutonomyDecision | null>(null)
  useEffect(() => {
    let cancelled = false
    Promise.resolve()
      .then(() => api.tasks.autonomyDecision(item.task_id))
      .then((d) => { if (!cancelled) setDecision(d ?? null) })
      .catch(() => { if (!cancelled) setDecision(null) })
    return () => { cancelled = true }
  }, [item.task_id, item.decision_id])

  const model = buildBannerModel(decision, {
    alwaysAsk: false,
    verification: item.verification,
    gateName: decision ? labelDisplayNameByName(decision.label, labels) : '',
  })
  const fallback = [
    ...(!item.risk_flags?.length ? ['no flags'] : []),
    ...(item.verification === 'pass' ? ['verify passed'] : []),
  ]
  const parts = model && model.kind === 'approve' ? model.summary : fallback
  if (parts.length === 0) return null
  return (
    <p data-testid="autonomy-reasons" className="text-xs text-fg-muted break-words">
      <span className="font-semibold text-fg">Why it qualified: </span>
      {parts.join(' · ')}
    </p>
  )
}
