import { Link } from 'react-router-dom'
import type { NextStep } from '../../lib/runTimeline'

const BTN = 'text-xs px-2.5 py-1 rounded bg-indigo-600 hover:bg-indigo-500 text-white shrink-0'

// Closing "Next step" line for a finished or paused run. The button is hidden
// when the matching handler isn't supplied (e.g. no Reply in the review layout).
export default function NextStepLine({ next, onReply, onRetry, onOpenDiff }: {
  next: NextStep
  onReply?: () => void
  onRetry?: () => void
  onOpenDiff?: () => void
}) {
  let action = null
  if (next.action === 'reply' && onReply) action = <button type="button" className={BTN} onClick={onReply}>Reply</button>
  else if (next.action === 'retry' && onRetry) action = <button type="button" className={BTN} onClick={onRetry}>Retry</button>
  else if (next.action === 'diff' && onOpenDiff) action = <button type="button" className={BTN} onClick={onOpenDiff}>Open Diff</button>
  else if (next.action === 'fix' && next.fix) {
    action = <Link to={next.fix.to} className={BTN}>{next.fix.text}</Link>
  }
  return (
    <div
      className="flex items-center gap-3 px-3 py-2 my-1 rounded-lg border border-indigo-800/40 bg-indigo-950/30 text-xs text-indigo-200"
      data-testid="next-step"
    >
      <span className="flex-1 min-w-0"><span className="font-semibold">Next step:</span> {next.text}</span>
      {action}
    </div>
  )
}
