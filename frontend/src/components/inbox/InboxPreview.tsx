import type { KeyboardEvent, ReactNode, RefObject } from 'react'
import { Link } from 'react-router-dom'
import type { Workflow } from '../../api/client'
import { entryTaskId, itemActions, type InboxEntry } from '../../lib/inbox'
import { needsMeReasonLabel } from '../../lib/needsMe'
import { escalationInfo } from '../../lib/escalation'
import { labelDisplayNameByName } from '../../lib/labelDisplay'
import { formatRelativeAge } from '../../lib/format'
import { sideEffectText } from '../shared/SideEffectBadge'
import { providerLabel } from '../shared/ProviderPausedBanner'
import ReviewBriefCard from '../task-detail/ReviewBriefCard'
import { rejectDisabled, rejectPlaceholder } from '../task-detail/TaskActions'
import DecisionContext from './DecisionContext'

const BTN = 'inline-flex items-center justify-center px-3 py-1.5 text-xs font-medium rounded text-white disabled:opacity-50'
const PRIMARY = `${BTN} bg-sky-600 hover:bg-sky-500`
const SECONDARY = `${BTN} bg-slate-700 hover:bg-slate-600 text-slate-100`
const KBD = 'ml-1.5 px-1 rounded border border-white/30 text-[10px] font-mono opacity-80'
const FIELD = 'flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-2.5 py-1.5 text-slate-200 placeholder-slate-500 focus:outline-none focus:border-slate-500'

export type InboxPreviewProps = {
  entry: InboxEntry | undefined
  workflow: Workflow | undefined
  busy: boolean
  error?: string
  noteOpen: boolean
  note: string
  setNote: (v: string) => void
  setNoteOpen: (v: boolean) => void
  noteRef: RefObject<HTMLTextAreaElement | null>
  reply: string
  setReply: (v: string) => void
  replyRef: RefObject<HTMLTextAreaElement | null>
  onApprove: () => void
  onReject: () => void
  onReply: () => void
  onRetry: () => void
  onDismissEffects: () => void
  onRetryEffects: () => void
  onApplySource: () => void
  onDiscardSource: () => void
  onResume: () => void
}

export default function InboxPreview(p: InboxPreviewProps) {
  const { entry, busy } = p
  if (!entry) {
    return (
      <div data-testid="inbox-preview" className="rounded-lg border border-slate-800 p-6 text-sm text-slate-500">
        Select an item
      </div>
    )
  }
  const taskId = entryTaskId(entry)
  const openTo = taskId ? `/tasks/${taskId}` : '/settings/health'

  let title = ''
  let chips: ReactNode = null
  let age = ''
  let body: ReactNode = null

  // Esc in a field closes it; the global hook ignores typing targets.
  const escClose = (close: () => void) => (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Escape') return
    e.preventDefault()
    close()
    e.currentTarget.blur()
  }

  switch (entry.kind) {
    case 'needs_me': {
      const item = entry.item
      const a = itemActions(item, p.workflow)
      const esc = escalationInfo(item.escalation_reason)
      title = item.task_title
      age = item.since
      chips = (
        <>
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-pink-900/60 text-pink-200 font-medium">
            {needsMeReasonLabel(item.reason, item.escalation_reason)}
          </span>
          {item.label && (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-700 text-slate-300">
              {labelDisplayNameByName(item.label, p.workflow?.labels)}
            </span>
          )}
        </>
      )
      const approveBtn = a.approve && (
        <button aria-label="Approve" title="Approve (A)" disabled={busy} onClick={p.onApprove} className={`${BTN} bg-emerald-600 hover:bg-emerald-500`}>
          Approve<kbd className={KBD}>a</kbd>
        </button>
      )
      if (item.reason === 'human_gate') {
        body = (
          <>
            <DecisionContext item={item} />
            <ReviewBriefCard taskId={item.task_id} refreshKey={item.since} filesOpen />
            <div className="flex flex-wrap gap-2">
              {approveBtn}
              {a.requestChanges && (
                <button
                  aria-label="Request changes" title="Request changes (R)"
                  disabled={busy}
                  onClick={() => p.setNoteOpen(!p.noteOpen)}
                  className={`${BTN} bg-red-700 hover:bg-red-600`}
                >
                  Request changes<kbd className={KBD}>r</kbd>
                </button>
              )}
            </div>
            {a.requestChanges && p.noteOpen && (
              <div className="flex gap-2" data-testid="request-changes-form">
                <textarea
                  ref={p.noteRef}
                  aria-label="Request changes note"
                  value={p.note}
                  onChange={(e) => p.setNote(e.target.value)}
                  onKeyDown={escClose(() => p.setNoteOpen(false))}
                  placeholder={rejectPlaceholder(0)}
                  rows={3}
                  className={FIELD}
                />
                <button disabled={rejectDisabled(p.note, 0, busy)} onClick={p.onReject} className={`${BTN} bg-red-700 hover:bg-red-600 self-start`}>
                  Send
                </button>
              </div>
            )}
          </>
        )
      } else if (item.reason === 'waiting_human' && a.reply) {
        body = (
          <>
            <p className="text-sm text-slate-200 whitespace-pre-wrap break-words">
              {item.message || `${esc.explanation} ${esc.nextStep}`}
            </p>
            {item.run_id ? (
              <div className="flex gap-2">
                <textarea
                  ref={p.replyRef}
                  aria-label="Reply to agent"
                  value={p.reply}
                  onChange={(e) => p.setReply(e.target.value)}
                  onKeyDown={escClose(() => undefined)}
                  placeholder="Reply to the agent…"
                  rows={4}
                  className={FIELD}
                />
                <button disabled={busy || !p.reply.trim()} onClick={p.onReply} className={`${PRIMARY} self-start`}>Send</button>
              </div>
            ) : (
              <Link to={openTo} className={PRIMARY}>Reply on the task page</Link>
            )}
            <div className="flex gap-2">{approveBtn}</div>
          </>
        )
      } else {
        const isConflict = item.reason === 'pr_conflict'
        body = (
          <>
            <p className="text-sm text-slate-300">
              {isConflict ? 'The pull request has a merge conflict and cannot continue until it is resolved.' : `${esc.explanation} ${esc.nextStep}`}
            </p>
            {item.message && <p className="text-xs text-slate-400 whitespace-pre-wrap break-words">{item.message}</p>}
            <div className="flex flex-wrap gap-2">
              {a.fix && <Link to={a.fix.to} className={PRIMARY}>{a.fix.text}</Link>}
              {a.retry && (
                <button disabled={busy} onClick={p.onRetry} className={a.fix ? SECONDARY : PRIMARY}>Retry</button>
              )}
              {approveBtn}
              {isConflict && <Link to={openTo} className={PRIMARY}>Open</Link>}
            </div>
          </>
        )
      }
      break
    }
    case 'source_edit': {
      title = entry.edit.task.title
      age = entry.edit.since
      body = (
        <>
          <p className="text-sm text-amber-300">
            The source issue was edited after approval. Review the new text before it reaches the agent.
          </p>
          <div className="flex flex-wrap gap-2">
            <Link to={openTo} className={SECONDARY}>Review</Link>
            <button disabled={busy} onClick={p.onDiscardSource} className={SECONDARY}>Discard</button>
            <button disabled={busy} onClick={p.onApplySource} className={PRIMARY}>Apply</button>
          </div>
        </>
      )
      break
    }
    case 'side_effect': {
      title = entry.failed.task.title
      age = entry.failed.since
      body = (
        <>
          {entry.failed.effects.map((ef) => (
            <p key={ef.kind} className="text-sm text-red-300 break-words">
              {sideEffectText(ef)}
              {ef.last_error ? `: ${ef.last_error}` : ''}
            </p>
          ))}
          <div className="flex flex-wrap gap-2">
            <button disabled={busy} onClick={p.onDismissEffects} className={SECONDARY}>Dismiss</button>
            <button disabled={busy} onClick={p.onRetryEffects} className={PRIMARY}>Retry</button>
          </div>
        </>
      )
      break
    }
    case 'provider': {
      const pv = entry.provider
      title = `${providerLabel(pv.provider)} dispatch is paused`
      age = pv.paused_at
      body = (
        <>
          <p className="text-sm text-slate-300">
            {pv.reason || 'The provider failed to authenticate.'} Queued tasks wait until it resumes.
          </p>
          <div className="flex gap-2">
            <button disabled={busy} onClick={p.onResume} className={PRIMARY}>Resume</button>
          </div>
        </>
      )
      break
    }
  }

  return (
    <div data-testid="inbox-preview" className="rounded-lg border border-slate-700 bg-slate-900 p-4 flex flex-col gap-3 min-w-0">
      <div className="flex flex-col gap-1.5 min-w-0">
        <h2 className="text-base font-semibold text-slate-100 break-words">{title}</h2>
        <div className="flex flex-wrap items-center gap-2">
          {chips}
          <span className="text-xs text-slate-500">{formatRelativeAge(age)}</span>
          <Link to={openTo} data-testid="inbox-preview-open" className="ml-auto text-xs text-sky-400 hover:underline">
            Open full page<kbd className="ml-1.5 px-1 rounded border border-slate-600 text-[10px] font-mono">↵</kbd>
          </Link>
        </div>
      </div>
      {body}
      {p.error && <p role="alert" className="text-xs text-red-400">{p.error}</p>}
    </div>
  )
}
