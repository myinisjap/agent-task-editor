/** Formats a duration in seconds as "Xm Ys" (or "Xs" under a minute). */
export function formatDuration(secs: number): string {
  if (!secs || secs <= 0) return '—'
  const mins = Math.floor(secs / 60)
  const rem = Math.round(secs % 60)
  return mins > 0 ? `${mins}m ${rem}s` : `${rem}s`
}

/**
 * Formats a future ISO timestamp as a short relative countdown ("in 5m",
 * "in 2h", "in 3d"). Used for BlockReason.clears_at (rate-limit reset,
 * retry backoff) so the user sees "how long to wait" rather than a raw
 * timestamp. Returns "now" for a timestamp at or before the current time
 * (e.g. a countdown that just elapsed but the page hasn't refetched yet).
 */
export function formatRelativeCountdown(iso: string, now: Date = new Date()): string {
  const target = new Date(iso).getTime()
  const diffMs = target - now.getTime()
  if (diffMs <= 0) return 'now'

  const diffSecs = Math.round(diffMs / 1000)
  if (diffSecs < 60) return `in ${diffSecs}s`
  const diffMins = Math.round(diffSecs / 60)
  if (diffMins < 60) return `in ${diffMins}m`
  const diffHours = Math.round(diffMins / 60)
  if (diffHours < 24) return `in ${diffHours}h`
  const diffDays = Math.round(diffHours / 24)
  return `in ${diffDays}d`
}

/**
 * Formats a past ISO timestamp as a short relative age ("just now", "5m ago",
 * "2h ago", "3d ago"). Future or unparseable timestamps read as "just now".
 */
export function formatRelativeAge(iso: string, now: Date = new Date()): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return 'just now'
  const secs = Math.round((now.getTime() - then) / 1000)
  if (secs < 60) return 'just now'
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}
