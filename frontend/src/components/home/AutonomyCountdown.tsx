import { useNow } from '../../lib/useNow'
import { formatElapsed } from '../../lib/homeInbox'
import { cx } from '../ui'

/** Live "approves in m:ss" to a pending auto-approval's due time. */
export default function AutonomyCountdown({ dueAt, className }: { dueAt: string | undefined; className?: string }) {
  const now = useNow(1000)
  if (!dueAt) return null
  const due = new Date(dueAt).getTime()
  if (Number.isNaN(due)) return null
  const left = due - now
  return (
    <span
      data-testid="autonomy-countdown"
      aria-live="off"
      title={`Approves at ${new Date(due).toLocaleTimeString()}`}
      className={cx('tabular-nums font-mono whitespace-nowrap', className)}
    >
      {left <= 0 ? 'approving…' : `approves in ${formatElapsed(left)}`}
    </span>
  )
}
