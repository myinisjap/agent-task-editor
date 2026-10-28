import type { Workflow } from '../../api/client'
import { labelStations } from '../../lib/homeInbox'
import { cx } from '../ui'

/** Slim one-segment-per-label pipeline; human gates are outlined in pink. */
export default function PipelineStrip({ workflow, labelCounts }: { workflow: Workflow; labelCounts: Record<string, number> }) {
  const stations = labelStations(workflow, labelCounts)
  return (
    <ul data-testid="pipeline-strip" className="flex flex-wrap gap-1.5 min-w-0" aria-label="Pipeline">
      {stations.map((s) => (
        <li
          key={s.key}
          data-gate={s.isGate ? 'true' : undefined}
          className={cx(
            'flex items-center gap-1.5 min-w-0 rounded-md bg-surface-raised px-2 py-1 text-xs text-fg border',
            s.isGate ? 'border-gate ring-1 ring-gate' : 'border-border',
          )}
        >
          <span aria-hidden className="inline-block w-1 h-4 rounded-full shrink-0" style={{ backgroundColor: s.color }} />
          <span className="truncate">{s.name}</span>
          <span className={cx('font-mono font-semibold', s.isGate && s.count > 0 && 'text-gate')}>{s.count}</span>
        </li>
      ))}
    </ul>
  )
}
