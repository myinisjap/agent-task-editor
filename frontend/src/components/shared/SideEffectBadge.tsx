import type { Task } from '../../api/client'

type SideEffect = NonNullable<Task['side_effects']>[number]

const NOUN: Record<string, string> = {
  create_pr: 'PR creation',
  terminal: 'Branch push',
  leave_not_ready: 'Issue update',
}

/** Short human text for one outstanding outbox row. */
export function sideEffectText(effect: SideEffect): string {
  const noun = NOUN[effect.kind] ?? effect.kind
  if (effect.status === 'failed') return `${noun} failed`
  if (effect.attempts > 0) return `${noun} failed, retrying`
  return `${noun} pending`
}

interface SideEffectBadgeProps {
  effects?: Task['side_effects']
  /** When set, a failed effect renders a Retry button that calls it. */
  onRetry?: () => void
}

/**
 * Shows the most pressing outstanding post-transition side effect (push, PR
 * creation, issue write-back): failed first, then retrying, then pending.
 * Renders nothing when the outbox is empty.
 */
export default function SideEffectBadge({ effects, onRetry }: SideEffectBadgeProps) {
  if (!effects || effects.length === 0) return null
  const rank = (e: SideEffect) => (e.status === 'failed' ? 0 : e.attempts > 0 ? 1 : 2)
  const worst = [...effects].sort((a, b) => rank(a) - rank(b))[0]
  const failed = worst.status === 'failed'
  const text = sideEffectText(worst)
  const title = worst.last_error ? `${text}: ${worst.last_error}` : text
  const tone = failed
    ? 'bg-red-900/60 text-red-300'
    : worst.attempts > 0
      ? 'bg-amber-900/60 text-amber-300'
      : 'bg-slate-700 text-slate-300'
  return (
    <span className="inline-flex items-center gap-1">
      <span
        className={`inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded font-medium ${tone}`}
        title={title}
        role="status"
      >
        {text}
      </span>
      {failed && onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="text-[10px] px-1.5 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-200"
        >
          Retry
        </button>
      )}
    </span>
  )
}
