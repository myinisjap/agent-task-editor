/** Formats a duration in seconds as "Xm Ys" (or "Xs" under a minute). */
export function formatDuration(secs: number): string {
  if (!secs || secs <= 0) return '—'
  const mins = Math.floor(secs / 60)
  const rem = Math.round(secs % 60)
  return mins > 0 ? `${mins}m ${rem}s` : `${rem}s`
}
