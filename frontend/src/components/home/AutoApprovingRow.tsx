import type { KeyboardEvent } from 'react'
import type { NeedsMeItem, Workflow } from '../../api/client'
import { formatChanged } from '../../lib/homeInbox'
import { humanPathTargetLabel } from '../../lib/humanGate'
import { labelDisplayName, labelDisplayNameByName } from '../../lib/labelDisplay'
import { Badge, Button } from '../ui'
import AutonomyCountdown from './AutonomyCountdown'

type Props = {
  item: NeedsMeItem
  workflow: Workflow | undefined
  busy: boolean
  entryKey: string
  onSelect: (el: HTMLElement) => void
  onHold: () => void
}

/** gate → target text for a pending auto-approval; the arrow is dropped when the target is unknown. */
export function gateTarget(item: NeedsMeItem, workflow: Workflow | undefined): string {
  const gate = labelDisplayNameByName(item.label, workflow?.labels)
  const target = humanPathTargetLabel(workflow, item.label, 'success')
  return target ? `${gate} → ${labelDisplayName(target)}` : gate
}

/** A pending auto-approval row: title, gate → target, chips, diff size, countdown and Hold. */
export default function AutoApprovingRow({ item, workflow, busy, entryKey, onSelect, onHold }: Props) {
  const diff = formatChanged(item.changed)
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.key !== 'h' || e.metaKey || e.ctrlKey || e.altKey) return
    e.preventDefault()
    if (!busy) onHold()
  }
  return (
    <li onKeyDown={onKeyDown} data-testid="auto-approving-row" className="min-w-0 flex items-center gap-1 rounded-md hover:bg-surface-sunken">
      <button
        type="button"
        data-entry-key={entryKey}
        onClick={(ev) => onSelect(ev.currentTarget)}
        className="flex-1 min-w-0 flex flex-col gap-0.5 rounded-md px-2 py-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      >
        <span className="truncate text-sm text-fg">{item.task_title}</span>
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-fg-muted">
          <span>{gateTarget(item, workflow)}</span>
          {!item.risk_flags?.length && <Badge tone="success" size="xs">no flags</Badge>}
          {item.verification === 'pass' && <Badge tone="success" size="xs">verify passed</Badge>}
          {diff && <span className="font-mono">{diff}</span>}
          <AutonomyCountdown dueAt={item.due_at} className="text-accent-fg" />
        </span>
      </button>
      <Button size="sm" variant="secondary" aria-label={`Hold ${item.task_title}`} disabled={busy} onClick={onHold} className="shrink-0 mr-1">
        Hold
      </Button>
    </li>
  )
}
