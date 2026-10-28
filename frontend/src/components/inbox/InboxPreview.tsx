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
import { Badge, Button, Card, Field, buttonClasses, controlClasses } from '../ui'

const PRIMARY = buttonClasses({ variant: 'primary', size: 'sm' })
const SECONDARY = buttonClasses({ variant: 'secondary', size: 'sm' })
const KBD = 'ml-1.5 px-1 rounded border border-current/40 text-[10px] font-mono opacity-80'
const FIELD = `${controlClasses} text-xs`

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
      <Card data-testid="inbox-preview" padding="none" className="p-6 text-sm text-fg-subtle">
        Select an item
      </Card>
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
          <Badge tone="accent" size="xs">
            {needsMeReasonLabel(item.reason, item.escalation_reason)}
          </Badge>
          {item.label && (
            <Badge tone="neutral" size="xs">
              {labelDisplayNameByName(item.label, p.workflow?.labels)}
            </Badge>
          )}
        </>
      )
      const approveBtn = a.approve && (
        <Button aria-label="Approve" title="Approve (A)" size="sm" variant="primary" disabled={busy} onClick={p.onApprove}>
          Approve<kbd className={KBD}>a</kbd>
        </Button>
      )
      if (item.reason === 'human_gate') {
        body = (
          <>
            <DecisionContext item={item} />
            <ReviewBriefCard taskId={item.task_id} refreshKey={item.since} filesOpen />
            <div className="flex flex-wrap gap-2">
              {approveBtn}
              {a.requestChanges && (
                <Button
                  aria-label="Request changes" title="Request changes (R)"
                  size="sm"
                  variant="danger"
                  disabled={busy}
                  onClick={() => p.setNoteOpen(!p.noteOpen)}
                >
                  Request changes<kbd className={KBD}>r</kbd>
                </Button>
              )}
            </div>
            {a.requestChanges && p.noteOpen && (
              <div className="flex gap-2" data-testid="request-changes-form">
                <Field label="Request changes note" labelHidden className="flex-1">
                  <textarea
                    ref={p.noteRef}
                    value={p.note}
                    onChange={(e) => p.setNote(e.target.value)}
                    onKeyDown={escClose(() => p.setNoteOpen(false))}
                    placeholder={rejectPlaceholder(0)}
                    rows={3}
                    className={FIELD}
                  />
                </Field>
                <Button size="sm" variant="danger" disabled={rejectDisabled(p.note, 0, busy)} onClick={p.onReject} className="self-start">
                  Send
                </Button>
              </div>
            )}
          </>
        )
      } else if (item.reason === 'waiting_human' && a.reply) {
        body = (
          <>
            <p className="text-sm text-fg whitespace-pre-wrap break-words">
              {item.message || `${esc.explanation} ${esc.nextStep}`}
            </p>
            {item.run_id ? (
              <div className="flex gap-2">
                <Field label="Reply to agent" labelHidden className="flex-1">
                  <textarea
                    ref={p.replyRef}
                    value={p.reply}
                    onChange={(e) => p.setReply(e.target.value)}
                    onKeyDown={escClose(() => undefined)}
                    placeholder="Reply to the agent…"
                    rows={4}
                    className={FIELD}
                  />
                </Field>
                <Button size="sm" variant="primary" disabled={busy || !p.reply.trim()} onClick={p.onReply} className="self-start">Send</Button>
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
            <p className="text-sm text-fg">
              {isConflict ? 'The pull request has a merge conflict and cannot continue until it is resolved.' : `${esc.explanation} ${esc.nextStep}`}
            </p>
            {item.message && <p className="text-xs text-fg-muted whitespace-pre-wrap break-words">{item.message}</p>}
            <div className="flex flex-wrap gap-2">
              {a.fix && <Link to={a.fix.to} className={PRIMARY}>{a.fix.text}</Link>}
              {a.retry && (
                <Button size="sm" variant={a.fix ? 'secondary' : 'primary'} disabled={busy} onClick={p.onRetry}>Retry</Button>
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
          <p className="text-sm text-warning">
            The source issue was edited after approval. Review the new text before it reaches the agent.
          </p>
          <div className="flex flex-wrap gap-2">
            <Link to={openTo} className={SECONDARY}>Review</Link>
            <Button size="sm" variant="secondary" disabled={busy} onClick={p.onDiscardSource}>Discard</Button>
            <Button size="sm" variant="primary" disabled={busy} onClick={p.onApplySource}>Apply</Button>
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
            <p key={ef.kind} className="text-sm text-danger break-words">
              {sideEffectText(ef)}
              {ef.last_error ? `: ${ef.last_error}` : ''}
            </p>
          ))}
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="secondary" disabled={busy} onClick={p.onDismissEffects}>Dismiss</Button>
            <Button size="sm" variant="primary" disabled={busy} onClick={p.onRetryEffects}>Retry</Button>
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
          <p className="text-sm text-fg">
            {pv.reason || 'The provider failed to authenticate.'} Queued tasks wait until it resumes.
          </p>
          <div className="flex gap-2">
            <Button size="sm" variant="primary" disabled={busy} onClick={p.onResume}>Resume</Button>
          </div>
        </>
      )
      break
    }
  }

  return (
    <Card data-testid="inbox-preview" padding="md" className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5 min-w-0">
        <h2 className="text-base font-semibold text-fg break-words">{title}</h2>
        <div className="flex flex-wrap items-center gap-2">
          {chips}
          <span className="text-xs text-fg-subtle">{formatRelativeAge(age)}</span>
          <Link to={openTo} data-testid="inbox-preview-open" className="ml-auto text-xs text-accent-fg hover:underline">
            Open full page<kbd className="ml-1.5 px-1 rounded border border-border text-[10px] font-mono">↵</kbd>
          </Link>
        </div>
      </div>
      {body}
      {p.error && <p role="alert" className="text-xs text-danger">{p.error}</p>}
    </Card>
  )
}
