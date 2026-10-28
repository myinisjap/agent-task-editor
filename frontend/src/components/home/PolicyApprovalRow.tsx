import type { PolicyApprovalItem, Workflow } from '../../api/client'
import { formatChanged, formatCompactAge, PR_STAYS_OPEN_TEXT, undoConfirmText } from '../../lib/homeInbox'
import { labelDisplayNameByName } from '../../lib/labelDisplay'
import { Button } from '../ui'

export const UNDO_DISABLED_TITLE = "Can't undo: merged or a run is active"

type Props = {
  approval: PolicyApprovalItem
  workflow: Workflow | undefined
  busy: boolean
  entryKey: string
  confirmOpen: boolean
  onSelect: (el: HTMLElement) => void
  onToggleConfirm: () => void
  onConfirmUndo: () => void
  onShouldHaveAsked?: () => void
}

/** A policy approval row (last 24h): time, gate → current label, diff size and Undo with an inline confirm. */
export default function PolicyApprovalRow({ approval: a, workflow, busy, entryKey, confirmOpen, onSelect, onToggleConfirm, onConfirmUndo, onShouldHaveAsked }: Props) {
  const gate = labelDisplayNameByName(a.gate_label, workflow?.labels)
  const current = labelDisplayNameByName(a.label, workflow?.labels)
  const diff = formatChanged(a.changed)
  return (
    <li data-testid="policy-approval-row" className="min-w-0 flex flex-col">
      <div className="flex flex-wrap items-center gap-1 rounded-md hover:bg-surface-sunken">
        <button
          type="button"
          data-entry-key={entryKey}
          onClick={(ev) => onSelect(ev.currentTarget)}
          className="flex-1 min-w-0 flex flex-col gap-0.5 rounded-md px-2 py-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
        >
          <span className="truncate text-sm text-fg">{a.task_title}</span>
          <span className="flex flex-wrap items-center gap-x-2 text-xs text-fg-muted">
            <span className="font-mono" title={new Date(a.approved_at).toLocaleString()}>{formatCompactAge(a.approved_at)}</span>
            <span>{gate} → {current}</span>
            {diff && <span className="font-mono">{diff}</span>}
          </span>
        </button>
        {onShouldHaveAsked && (
          <Button
            size="sm"
            variant="ghost"
            aria-label={`Should have asked about ${a.task_title}`}
            onClick={onShouldHaveAsked}
            className="shrink-0"
          >
            Should have asked
          </Button>
        )}
        <Button
          size="sm"
          variant="secondary"
          aria-label={`Undo ${a.task_title}`}
          disabled={busy || !a.undoable}
          title={a.undoable ? undefined : UNDO_DISABLED_TITLE}
          onClick={onToggleConfirm}
          className="shrink-0 mr-1"
        >
          Undo
        </Button>
      </div>
      {confirmOpen && a.undoable && (
        <div
          role="group"
          aria-label="Confirm undo"
          data-testid="undo-confirm"
          onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); onToggleConfirm() } }}
          className="mx-2 mb-1 flex flex-col gap-2 rounded-md border border-border bg-surface-sunken p-2 text-xs text-fg"
        >
          <p>{undoConfirmText(a, { gate, current })}</p>
          {a.pr_url && (
            <p>{PR_STAYS_OPEN_TEXT} <a href={a.pr_url} target="_blank" rel="noreferrer" className="text-accent-fg hover:underline">View PR</a></p>
          )}
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="danger" disabled={busy} onClick={onConfirmUndo}>Undo approval</Button>
            <Button size="sm" variant="secondary" onClick={onToggleConfirm}>Cancel</Button>
          </div>
        </div>
      )}
    </li>
  )
}
