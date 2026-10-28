import { MessageSquare } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { AgentRun } from '../../api/client'
import { escalationInfo } from '../../lib/escalation'
import { Button, Field, buttonClasses, controlClasses, Icon } from '../ui'

const MOBILE_TAP = 'max-md:min-h-[44px]'
const AREA = `${controlClasses} flex-1 min-w-0 text-base md:text-xs resize-none`

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
      className="shrink-0 border-t border-border bg-surface-raised p-4 max-md:sticky max-md:bottom-0 max-md:z-10 max-md:max-h-[55vh] max-md:overflow-y-auto max-md:mb-[var(--ate-bottom-bar-height,0px)] max-md:min-w-0">
      <p className="text-sm font-medium text-fg mb-3">
        {needsHuman ? esc.label : 'Human review required'}
      </p>
      {needsHuman && (
        <div className="text-xs text-fg-muted mb-3 space-y-0.5">
          <p>{esc.explanation}</p>
          <p>Next: {esc.nextStep}</p>
        </div>
      )}
      {message && (
        <p className="text-xs text-fg-muted mb-3 bg-surface-sunken rounded p-2 whitespace-pre-wrap">
          {message}
        </p>
      )}
      {needsHuman && !replyIsPrimary && (
        <div className="flex flex-wrap gap-3 items-center mb-3">
          {esc.action === 'fix' && esc.fix && (
            <Link
              to={esc.fix.to}
              className={buttonClasses({ variant: 'primary', size: 'sm', className: MOBILE_TAP })}
            >
              {esc.fix.text}
            </Link>
          )}
          {onRetry && (
            <Button
              size="sm"
              variant={esc.action === 'retry' ? 'primary' : 'secondary'}
              onClick={onRetry}
              disabled={actionPending}
              className={MOBILE_TAP}
            >
              {esc.action === 'fix' ? 'Retry after fixing' : 'Retry'}
            </Button>
          )}
        </div>
      )}
      {openCommentsCount > 0 && (
        <p className="text-xs text-warning mb-2">
          <Icon icon={MessageSquare} size="sm" className="inline -mt-0.5 mr-1" />{openCommentsCount} open diff comment{openCommentsCount !== 1 ? 's' : ''} — the next agent run will see and address them
          {' '}
          <button
            type="button"
            onClick={onJumpToDiffTab}
            className="underline hover:text-fg"
          >
            review in Diff tab
          </button>
        </p>
      )}
      {needsHuman && (
        <div className="flex flex-col md:flex-row gap-3 items-stretch md:items-start mb-3">
          <Field label="Reply to the agent" labelHidden className="flex-1 min-w-0">
            <textarea
              id="task-reply-input"
              value={replyText}
              onChange={(e) => setReplyText(e.target.value)}
              placeholder="Reply to the agent — answer its question and it continues in the same session, without moving the task…"
              rows={2}
              className={AREA}
            />
          </Field>
          <Button
            size="sm"
            variant={replyIsPrimary ? 'primary' : 'secondary'}
            onClick={onReply}
            disabled={actionPending || !replyText.trim()}
            className={MOBILE_TAP}
          >
            Reply & Continue
          </Button>
        </div>
      )}
      {actionError && (
        <p role="alert" className="text-xs text-danger mb-3">{actionError}</p>
      )}
      {(canApprove || canReject) && (
      <div className="flex flex-col md:flex-row gap-3 items-stretch md:items-start">
        {canReject ? (
        <Field label="Rejection note" labelHidden className="flex-1 min-w-0">
          <textarea
            value={rejectNote}
            data-reject-note
            onChange={(e) => setRejectNote(e.target.value)}
            placeholder={
              openCommentsCount > 0
                ? 'Additional rejection note (optional — open inline comments reach the agent automatically)…'
                : 'Rejection note (required to reject)…'
            }
            rows={2}
            className={AREA}
          />
        </Field>
        ) : (
          <div className="flex-1" />
        )}
        <div className="flex flex-row md:flex-col gap-2 [&>button]:flex-1 md:[&>button]:flex-none">
          {canApprove && (
          <Button size="sm" variant="primary" onClick={onApprove} disabled={actionPending} className={MOBILE_TAP}>
            Approve
          </Button>
          )}
          {canReject && (
          <Button
            size="sm"
            variant="danger"
            onClick={onReject}
            disabled={rejectDisabled(rejectNote, openCommentsCount, actionPending)}
            className={MOBILE_TAP}
          >
            Reject
          </Button>
          )}
        </div>
      </div>
      )}
    </div>
  )
}
