import type { AgentRun } from '../../api/client'

// TaskActions renders the approval panel shown when the agent needs human
// input or the task sits at a human-gate label: feedback, open-comment
// banner, reply box (waiting_human only), and approve/reject controls.
export default function TaskActions({
  activeRun,
  needsHuman,
  openCommentsCount,
  replyText,
  setReplyText,
  onReply,
  rejectNote,
  setRejectNote,
  onReject,
  onApprove,
  actionPending,
  onJumpToDiffTab,
}: {
  activeRun: AgentRun | undefined
  needsHuman: boolean
  openCommentsCount: number
  replyText: string
  setReplyText: (v: string) => void
  onReply: () => void
  rejectNote: string
  setRejectNote: (v: string) => void
  onReject: () => void
  onApprove: () => void
  actionPending: boolean
  onJumpToDiffTab: () => void
}) {
  return (
    <div className="shrink-0 border-t border-slate-700 bg-slate-900 p-4">
      <p className="text-sm font-medium text-slate-200 mb-3">
        {needsHuman ? 'Agent is waiting for your input' : 'Human review required'}
      </p>
      {activeRun?.feedback && (
        <p className="text-xs text-slate-400 mb-3 bg-slate-800 rounded p-2">
          {activeRun.feedback}
        </p>
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
        <div className="flex gap-3 items-start mb-3">
          <textarea
            value={replyText}
            onChange={(e) => setReplyText(e.target.value)}
            placeholder="Reply to the agent — answer its question and it continues in the same session, without moving the task…"
            rows={2}
            className="flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500"
          />
          <button
            onClick={onReply}
            disabled={actionPending || !replyText.trim()}
            className="px-4 py-1.5 text-xs font-medium rounded bg-sky-600 hover:bg-sky-500 text-white disabled:opacity-50"
          >
            Reply & Continue
          </button>
        </div>
      )}
      <div className="flex gap-3 items-start">
        <textarea
          value={rejectNote}
          onChange={(e) => setRejectNote(e.target.value)}
          placeholder={
            openCommentsCount > 0
              ? 'Additional rejection note (optional — open inline comments reach the agent automatically)…'
              : 'Rejection note (required to reject)…'
          }
          rows={2}
          className="flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500"
        />
        <div className="flex flex-col gap-2">
          <button
            onClick={onApprove}
            disabled={actionPending}
            className="px-4 py-1.5 text-xs font-medium rounded bg-emerald-600 hover:bg-emerald-500 text-white disabled:opacity-50"
          >
            Approve
          </button>
          <button
            onClick={onReject}
            disabled={actionPending || (!rejectNote.trim() && openCommentsCount === 0)}
            className="px-4 py-1.5 text-xs font-medium rounded bg-red-700 hover:bg-red-600 text-white disabled:opacity-50"
          >
            Reject
          </button>
        </div>
      </div>
    </div>
  )
}
