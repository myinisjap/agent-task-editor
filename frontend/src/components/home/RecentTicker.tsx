import { formatTickerTime, type TickerItem } from '../../lib/homeTicker'
import { cx } from '../ui'

export default function RecentTicker({ items, latestOnly }: { items: TickerItem[]; latestOnly?: boolean }) {
  const shown = (latestOnly ? items.slice(0, 1) : items.slice(0, 3))
  return (
    <div
      role="log"
      aria-live="polite"
      aria-label="Recent activity"
      data-testid="recent-ticker"
      className="flex items-center gap-3 min-w-0 border-t border-border pt-2 text-xs"
    >
      {shown.length === 0 ? (
        <span className="text-fg-subtle">No activity yet</span>
      ) : (
        shown.map((it, i) => (
          <div
            key={it.id}
            data-testid="ticker-item"
            className={cx(
              'flex items-baseline gap-2 min-w-0 rounded px-1.5 py-0.5',
              i === 0 ? 'home-ticker-new' : '',
              i > 0 && 'border-l border-border pl-3',
              !latestOnly && 'flex-1',
            )}
          >
            <span className="shrink-0 font-mono text-fg-muted">{formatTickerTime(it.at)}</span>
            <span className={cx('min-w-0 truncate', it.tone === 'gate' ? 'text-gate' : 'text-fg')}>{it.text}</span>
          </div>
        ))
      )}
    </div>
  )
}
