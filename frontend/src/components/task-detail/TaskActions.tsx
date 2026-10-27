import { Link } from 'react-router-dom'
import type { AgentRun } from '../../api/client'
import { escalationInfo } from '../../lib/escalation'

// Reject needs a note or at least one open diff comment. Shared with ReviewLayout.
export function rejectDisabled(rejectNote: string, openCommentsCount: number, actionPending: boolean): boolean {
  return actionPending || (!rejectNote.trim() && openCommentsCount === 0)
}

export function rejectPlaceholder(openCommentsCount: number): string {
  return openCommentsCount > 0
    ? 'Additional rejection note (optional — open inline comments reach the agent automatically)…'
    : 'Rejection note (required to reject)…'
}

// TaskActions renders the approval panel shown when the agent needs human
// input or the task sits at a human-gate label: the escalation reason and its
// message, open-comment banner, reply box (waiting_human only), approve/reject
// controls, and — for failures rather than questions — Retry / a settings link
// as the primary action (chosen from the run's escalation_reason).
export default function TaskActions({
  activeRun,
  needsHuman,
  openCommentsCount,
  replyText,
  setReplyText,
  onReply,
  onRetry,
  rejectNote,
  setRejectNote,
  onReject,
  onApprove,
  canApprove,
  canReject,
  actionError,
  actionPending,
  onJumpToDiffTab,
}: {
  activeRun: AgentRun | undefined
  needsHuman: boolean
  openCommentsCount: number
  replyText: string
  setReplyText: (v: string) => void
  onReply: () => void
  // Re-dispatches the task without a reply (used for failure escalations).
  onRetry?: () => void
  rejectNote: string
  setRejectNote: (v: string) => void
  onReject: () => void
  onApprove: () => void
  // Whether the workflow defines a human success/failure transition from the
  // task's current label; Approve/Reject are hidden when it doesn't.
  canApprove: boolean
  canReject: boolean
  actionError?: string
  actionPending: boolean
  onJumpToDiffTab: () => void
}) {
  const esc = escalationInfo(activeRun?.escalation_reason)
  // The stored explanation: the agent's own question lands in feedback, every
  // other escalation reason in notes.
  const message = needsHuman ? (activeRun?.feedback || activeRun?.notes) : activeRun?.feedback
  const replyIsPrimary = esc.action === 'reply'
  return (
    <div data-testid="task-actions-bar"
      className="shrink-0 border-t border-slate-700 bg-slate-900 p-4 max-md:sticky max-md:bottom-0 max-md:z-10 max-md:max-h-[55vh] max-md:overflow-y-auto max-md:mb-[var(--ate-bottom-bar-height,0px)] max-md:min-w-0">
      <p className="text-sm font-medium text-slate-200 mb-3">
        {needsHuman ? esc.label : 'Human review required'}
      </p>
      {needsHuman && (
        <div className="text-xs text-slate-400 mb-3 space-y-0.5">
          <p>{esc.explanation}</p>
          <p>Next: {esc.nextStep}</p>
        </div>
      )}
      {message && (
        <p className="text-xs text-slate-400 mb-3 bg-slate-800 rounded p-2 whitespace-pre-wrap">
          {message}
        </p>
      )}
      {needsHuman && !replyIsPrimary && (
        <div className="flex flex-wrap gap-3 items-center mb-3">
          {esc.action === 'fix' && esc.fix && (
            <Link
              to={esc.fix.to}
              className="px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-sky-600 hover:bg-sky-500 text-white"
            >
              {esc.fix.text}
            </Link>
          )}
          {onRetry && (
            <button
              onClick={onRetry}
              disabled={actionPending}
              className={
                esc.action === 'retry'
                  ? 'px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-sky-600 hover:bg-sky-500 text-white disabled:opacity-50'
                  : 'px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-100 disabled:opacity-50'
              }
            >
              {esc.action === 'fix' ? 'Retry after fixing' : 'Retry'}
            </button>
          )}
        </div>
      )}
      {openCommentsCount > 0 && (
        <p className="text-xs text-amber-400 mb-2">
          💬 {openCommentsCount} open diff comment{openCommentsCount !== 1 ? 's' : ''} — the next agent run will see and address them
          {' '}
          <button
            onClick={onJumpToDiffTab}
            className="underline hover:text-amber-300"
          >
            review in Diff tab
          </button>
        </p>
      )}
      {needsHuman && (
        <div className="flex flex-col md:flex-row gap-3 items-stretch md:items-start mb-3">
          <textarea
            value={replyText}
            onChange={(e) => setReplyText(e.target.value)}
            placeholder="Reply to the agent — answer its question and it continues in the same session, without moving the task…"
            rows={2}
            className="flex-1 min-w-0 text-base md:text-xs bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500"
          />
          <button
            onClick={onReply}
            disabled={actionPending || !replyText.trim()}
            className={
              replyIsPrimary
                ? 'px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-sky-600 hover:bg-sky-500 text-white disabled:opacity-50'
                : 'px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-100 disabled:opacity-50'
            }
          >
            Reply & Continue
          </button>
        </div>
      )}
      {actionError && (
        <p role="alert" className="text-xs text-red-400 mb-3">{actionError}</p>
      )}
      {(canApprove || canReject) && (
      <div className="flex flex-col md:flex-row gap-3 items-stretch md:items-start">
        {canReject ? (
        <textarea
          value={rejectNote}
          onChange={(e) => setRejectNote(e.target.value)}
          placeholder={
            openCommentsCount > 0
              ? 'Additional rejection note (optional — open inline comments reach the agent automatically)…'
              : 'Rejection note (required to reject)…'
          }
          rows={2}
          className="flex-1 min-w-0 text-base md:text-xs bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500"
        />
        ) : (
          <div className="flex-1" />
        )}
        <div className="flex flex-row md:flex-col gap-2 [&>button]:flex-1 md:[&>button]:flex-none">
          {canApprove && (
          <button
            onClick={onApprove}
            disabled={actionPending}
            className="px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-emerald-600 hover:bg-emerald-500 text-white disabled:opacity-50"
          >
            Approve
          </button>
          )}
          {canReject && (
          <button
            onClick={onReject}
            disabled={rejectDisabled(rejectNote, openCommentsCount, actionPending)}
            className="px-4 py-1.5 max-md:min-h-[44px] text-xs font-medium rounded bg-red-600 hover:bg-red-500 text-white disabled:opacity-50"
          >
            Reject
          </button>
          )}
        </div>
      </div>
      )}
    </div>
  )
}
