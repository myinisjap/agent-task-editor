import { useState } from 'react'
import type { Workflow } from '../../api/client'
import { entryKey, entrySince, inboxCount, SECTION_META, type InboxEntry, type InboxSections } from '../../lib/inbox'
import { countedNonEmptySections, entryTitle, formatCompactAge, isStale, oldestSince, sectionTitle } from '../../lib/homeInbox'
import { Badge, Card, cx } from '../ui'
import AutoApprovingRow from './AutoApprovingRow'
import PolicyApprovalRow from './PolicyApprovalRow'

type Props = {
  sections: InboxSections
  /** Single hook for row activation: opens the review popup. `el` is the clicked button (focus return). */
  onSelect: (entry: InboxEntry, el?: HTMLElement) => void
  /** Show only the first N rows plus a "Review all" link (phone). */
  limit?: number
  /** Tint the panel (a new item needs you). Stays under reduced motion. */
  highlight?: boolean
  workflows?: Workflow[]
  /** Hold a pending auto-approval. */
  onHold?: (entry: InboxEntry) => void
  /** Undo a policy approval (after the inline confirm). */
  onUndo?: (entry: InboxEntry) => void
  isBusy?: (entry: InboxEntry) => boolean
}

export default function NeedsYouPanel({ sections, onSelect, limit, highlight, workflows = [], onHold, onUndo, isBusy }: Props) {
  const [approvedOpen, setApprovedOpen] = useState<boolean | undefined>(undefined)
  const [confirmKey, setConfirmKey] = useState<string | null>(null)
  const count = inboxCount(sections)
  const groups = countedNonEmptySections(sections)
  const rows = groups.flatMap(([key, entries]) => entries.map((e) => ({ key, e })))
  const shown = limit ? rows.slice(0, limit) : rows
  const oldest = oldestSince(sections)
  const now = Date.now()
  const pending = sections.auto_approving
  const approved = sections.approved
  const expanded = approvedOpen ?? approved.length > 0
  const wf = (id: string) => workflows.find((w) => w.id === id)
  const busyOf = (e: InboxEntry) => !!isBusy?.(e)
  return (
    <Card as="section" padding="md" aria-label="Needs you" data-testid="needs-you-panel" data-flash={highlight ? "true" : undefined} className={cx("flex flex-col gap-3 transition-colors", highlight && "ring-2 ring-gate bg-gate-soft/40")}>
      <div className="flex items-baseline gap-3 min-w-0">
        <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide">Needs you</h2>
      </div>
      <div className="flex items-baseline gap-3 min-w-0">
        <span data-testid="needs-you-count" className="text-6xl xl:text-7xl leading-none tabular-nums font-semibold text-fg font-mono">{count}</span>
        <div className="min-w-0 text-xs text-fg-muted">
          {count > 0 ? (
            <>
              <p data-testid="needs-you-summary" className="truncate">
                {groups.map(([k, v]) => `${sectionTitle(k)} ${v.length}`).join(' · ')}
              </p>
              {oldest && <p>oldest {formatCompactAge(oldest, now)}</p>}
            </>
          ) : (
            <p>Nothing needs you.</p>
          )}
        </div>
      </div>
      {shown.length > 0 && (
        <ul className={cx('flex flex-col gap-1 min-w-0', !limit && 'max-h-[30vh] overflow-y-auto')}>
          {shown.map(({ key, e }) => {
            const since = entrySince(e)
            return (
              <li key={entryKey(e)} className="min-w-0">
                <button
                  type="button"
                  data-entry-key={entryKey(e)}
                  onClick={(ev) => onSelect(e, ev.currentTarget)}
                  className="w-full flex items-center gap-2 min-w-0 rounded-md px-2 py-1.5 text-left hover:bg-surface-sunken focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                >
                  <Badge tone={SECTION_META[key]?.tone ?? 'neutral'} size="xs" className="shrink-0">{sectionTitle(key)}</Badge>
                  <span className="flex-1 min-w-0 truncate text-sm text-fg">{entryTitle(e)}</span>
                  <span
                    data-stale={isStale(since, now) ? 'true' : undefined}
                    className={cx('shrink-0 font-mono text-xs', isStale(since, now) ? 'text-warning' : 'text-fg-muted')}
                  >
                    {formatCompactAge(since, now)}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {pending.length > 0 && (
        <div data-testid="auto-approving-group" className="flex flex-col gap-1">
          <h3 className="flex items-center gap-2 text-xs font-medium text-fg-subtle uppercase tracking-wide">
            <Badge tone={SECTION_META.auto_approving.tone} size="xs">{SECTION_META.auto_approving.title}</Badge>
            <span className="normal-case tracking-normal font-normal truncate">{SECTION_META.auto_approving.subtitle}</span>
          </h3>
          <ul className="flex flex-col gap-1 min-w-0">
            {pending.map((e) => e.kind === 'needs_me' && (
              <AutoApprovingRow
                key={entryKey(e)}
                item={e.item}
                workflow={wf(e.item.workflow_id)}
                busy={busyOf(e)}
                entryKey={entryKey(e)}
                onSelect={(el) => onSelect(e, el)}
                onHold={() => onHold?.(e)}
              />
            ))}
          </ul>
        </div>
      )}
      <div data-testid="approved-group" className="flex flex-col gap-1">
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setApprovedOpen(!expanded)}
          className="self-start text-xs font-medium text-fg-subtle hover:text-fg uppercase tracking-wide"
        >
          {expanded ? '▾' : '▸'} {SECTION_META.approved.title} · {approved.length}
        </button>
        {expanded && approved.length > 0 && (
          <ul className="flex flex-col gap-1 min-w-0">
            {approved.map((e) => e.kind === 'policy_approval' && (
              <PolicyApprovalRow
                key={entryKey(e)}
                approval={e.approval}
                workflow={wf(e.approval.workflow_id)}
                busy={busyOf(e)}
                entryKey={entryKey(e)}
                confirmOpen={confirmKey === entryKey(e)}
                onSelect={(el) => onSelect(e, el)}
                onToggleConfirm={() => setConfirmKey(confirmKey === entryKey(e) ? null : entryKey(e))}
                onConfirmUndo={() => { setConfirmKey(null); onUndo?.(e) }}
              />
            ))}
          </ul>
        )}
      </div>
      {limit !== undefined && count > shown.length && (
        <button
          type="button"
          onClick={(ev) => onSelect(rows[0].e, ev.currentTarget)}
          className="self-start text-sm text-accent-fg hover:underline"
        >
          Review all {count}
        </button>
      )}
    </Card>
  )
}
