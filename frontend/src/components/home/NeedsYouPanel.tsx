import { Link } from 'react-router-dom'
import { entryKey, entrySince, inboxCount, type InboxEntry, type InboxSections } from '../../lib/inbox'
import { entryTitle, formatCompactAge, isStale, nonEmptySections, oldestSince, sectionTitle } from '../../lib/homeInbox'
import { Badge, Card, cx, type BadgeTone } from '../ui'

const TONE: Record<string, BadgeTone> = { decide: 'accent', answer: 'info', fix: 'warning' }

type Props = {
  sections: InboxSections
  /** Single hook for row activation; the Inbox popup will replace the navigation. */
  onSelect: (entry: InboxEntry) => void
  /** Show only the first N rows plus a "Review all" link (phone). */
  limit?: number
}

export default function NeedsYouPanel({ sections, onSelect, limit }: Props) {
  const count = inboxCount(sections)
  const groups = nonEmptySections(sections)
  const rows = groups.flatMap(([key, entries]) => entries.map((e) => ({ key, e })))
  const shown = limit ? rows.slice(0, limit) : rows
  const oldest = oldestSince(sections)
  const now = Date.now()
  return (
    <Card as="section" padding="md" aria-label="Needs you" data-testid="needs-you-panel" className="flex flex-col gap-3">
      <div className="flex items-baseline gap-3 min-w-0">
        <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide">Needs you</h2>
      </div>
      <div className="flex items-baseline gap-3 min-w-0">
        <span data-testid="needs-you-count" className="text-4xl font-semibold text-fg font-mono">{count}</span>
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
        <ul className={cx('flex flex-col gap-1 min-w-0', !limit && 'max-h-72 overflow-y-auto')}>
          {shown.map(({ key, e }) => {
            const since = entrySince(e)
            return (
              <li key={entryKey(e)} className="min-w-0">
                <button
                  type="button"
                  onClick={() => onSelect(e)}
                  className="w-full flex items-center gap-2 min-w-0 rounded-md px-2 py-1.5 text-left hover:bg-surface-sunken focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                >
                  <Badge tone={TONE[key] ?? 'neutral'} size="xs" className="shrink-0">{sectionTitle(key)}</Badge>
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
      {limit !== undefined && count > shown.length && (
        <Link to="/inbox" className="text-sm text-accent-fg hover:underline">Review all {count}</Link>
      )}
    </Card>
  )
}
