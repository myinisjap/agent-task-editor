import { labelDisplayNameByName } from '../lib/labelDisplay'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type MouseEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api/client'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore, useInboxSections } from '../stores/inbox'
import DecisionContext from '../components/inbox/DecisionContext'
import InboxPreview from '../components/inbox/InboxPreview'
import { useIsWideDesktop } from '../lib/useMediaQuery'
import { useInboxKeyboard, type InboxKeyAction } from '../lib/inboxKeys'
import { useHealthStore } from '../stores/health'
import { useWorkflowStore } from '../stores/workflow'
import { entryKey, entryTaskId, flattenInbox, inboxCount, itemActions, neighborKey, nextAfterRemoval, resolveSelection, SECTION_ORDER, type InboxEntry, type InboxSectionKey } from '../lib/inbox'
import { needsMeReasonLabel } from '../lib/needsMe'
import { escalationInfo } from '../lib/escalation'
import { formatRelativeAge } from '../lib/format'
import { sideEffectText } from '../components/shared/SideEffectBadge'
import { providerLabel } from '../components/shared/ProviderPausedBanner'
import type { NeedsMeItem } from '../api/client'
import { Badge, Button, Card, Field, PageFrame, buttonClasses, controlClasses } from '../components/ui'

const BTN = 'flex-1 md:flex-none min-h-[44px] md:min-h-0'
const linkBtn = (variant: 'primary' | 'secondary') => buttonClasses({ variant, size: 'sm', className: BTN })

const SECTION_META: Record<InboxSectionKey, { title: string; subtitle: string }> = {
  decide: { title: 'Decide', subtitle: 'Approve or request changes' },
  answer: { title: 'Answer', subtitle: 'The agent asked something' },
  fix: { title: 'Fix setup', subtitle: 'Something needs fixing before work can continue' },
}

function Section({ id, count, children }: { id: InboxSectionKey; count: number; children: ReactNode }) {
  if (count === 0) return null
  const { title, subtitle } = SECTION_META[id]
  return (
    <section className="mb-8" data-testid={`inbox-section-${id}`}>
      <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide mb-1">
        {title} ({count})
      </h2>
      <p className="text-xs text-fg-subtle mb-3">{subtitle}</p>
      <div className="flex flex-col gap-3">{children}</div>
    </section>
  )
}

const isPhone = () => typeof window !== 'undefined' && !!window.matchMedia && window.matchMedia('(max-width: 767px)').matches

function Row({ children, error, to, extra, selected, onSelect }: { children: ReactNode; error?: string; to?: string; extra?: ReactNode; selected?: boolean; onSelect?: () => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView?.({ block: 'nearest' })
  }, [selected])
  const navigate = useNavigate()
  // On phones the whole row taps through to the task; taps on inner links/buttons keep their own action.
  const onClick = (e: MouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest('a,button,input,textarea,select')) return
    if (onSelect) {
      onSelect()
      return
    }
    if (!to || !isPhone()) return
    navigate(to)
  }
  return (
    <Card
      ref={ref}
      padding="md"
      selected={selected}
      interactive={!!onSelect}
      data-testid="inbox-item"
      data-selected={selected ? 'true' : undefined}
      aria-current={selected ? 'true' : undefined}
      onClick={onClick}
    >
      <div className="flex flex-col md:flex-row md:items-start md:justify-between gap-3 md:gap-4">{children}</div>
      {extra}
      {error && <p role="alert" className="text-xs text-danger mt-2">{error}</p>}
    </Card>
  )
}

export default function InboxPage() {
  const refreshNeedsMe = useNeedsMeStore((s) => s.refresh)
  const refreshEffects = useInboxStore((s) => s.refresh)
  const resume = useHealthStore((s) => s.resume)
  const fetchHealth = useHealthStore((s) => s.fetch)
  const workflows = useWorkflowStore((s) => s.workflows)
  const sections = useInboxSections()
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [noteOpen, setNoteOpen] = useState<Record<string, boolean>>({})
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [replies, setReplies] = useState<Record<string, string>>({})
  const [legend, setLegend] = useState(false)
  const [focusTick, setFocusTick] = useState(0)
  const noteRef = useRef<HTMLTextAreaElement>(null)
  const replyRef = useRef<HTMLTextAreaElement>(null)
  const navigate = useNavigate()
  const isWide = useIsWideDesktop()
  const [searchParams, setSearchParams] = useSearchParams()
  const lastSel = useRef<{ key?: string; index: number }>({ index: 0 })

  useEffect(() => {
    void refreshNeedsMe()
    void refreshEffects()
    void fetchHealth()
    if (useWorkflowStore.getState().workflows.length === 0) void useWorkflowStore.getState().fetch()
  }, [refreshNeedsMe, refreshEffects, fetchHealth])

  const total = inboxCount(sections)
  const flat = useMemo(() => flattenInbox(sections), [sections])
  const param = searchParams.get('task')
  const selectedKey = isWide ? resolveSelection(flat, param) : undefined
  const selectedEntry = selectedKey ? flat.find((e) => entryKey(e) === selectedKey) : undefined

  const select = useCallback((key: string | undefined) => {
    if (!key) return
    setSearchParams((p) => {
      p.set('task', key)
      return p
    }, { replace: true })
  }, [setSearchParams])

  // Keep the URL in step with the selection; when the selected entry vanishes, select its neighbour.
  useEffect(() => {
    if (!isWide || flat.length === 0) return
    const exists = !!param && flat.some((e) => entryKey(e) === param || entryTaskId(e) === param)
    let target = resolveSelection(flat, param)
    if (!exists && param && lastSel.current.key === param) {
      target = entryKey(flat[Math.min(lastSel.current.index, flat.length - 1)])
    }
    if (!target) return
    lastSel.current = { key: target, index: flat.findIndex((e) => entryKey(e) === target) }
    if (target !== param) select(target)
  }, [isWide, flat, param, select])

  useEffect(() => {
    if (focusTick > 0) noteRef.current?.focus()
  }, [focusTick])

  // Resolves true on success. When `advance` is given (wide layout), selection moves there afterwards.
  const run = async (key: string, fn: () => Promise<unknown>, after: () => Promise<unknown> | void, advance?: string): Promise<boolean> => {
    setErrors((e) => ({ ...e, [key]: '' }))
    setBusy((b) => ({ ...b, [key]: true }))
    try {
      await fn()
      await after()
      if (isWide && advance) select(advance)
      return true
    } catch (e) {
      setErrors((m) => ({ ...m, [key]: e instanceof Error ? e.message : String(e) }))
      return false
    } finally {
      setBusy((b) => ({ ...b, [key]: false }))
    }
  }

  const refreshAll = () => Promise.all([refreshNeedsMe(), refreshEffects()])

  const sendChanges = (id: string, advance?: string) =>
    run(id, async () => {
      await api.tasks.reject(id, (notes[id] ?? '').trim())
      setNotes((n) => ({ ...n, [id]: '' }))
      setNoteOpen((o) => ({ ...o, [id]: false }))
    }, refreshAll, advance)

  const approve = (id: string, advance?: string) => run(id, () => api.tasks.approve(id), refreshAll, advance)

  const sendReply = (item: NeedsMeItem, advance?: string) =>
    run(item.task_id, async () => {
      await api.tasks.replyRun(item.task_id, item.run_id as string, (replies[item.task_id] ?? '').trim())
      setReplies((r) => ({ ...r, [item.task_id]: '' }))
    }, refreshAll, advance)

  const onKey = (action: InboxKeyAction) => {
    switch (action) {
      case 'next': return select(neighborKey(flat, selectedKey, 1))
      case 'prev': return select(neighborKey(flat, selectedKey, -1))
      case 'help': return setLegend((v) => !v)
      case 'open': {
        if (!selectedEntry) return
        const id = entryTaskId(selectedEntry)
        return navigate(id ? `/tasks/${id}` : '/settings/health')
      }
      case 'escape': {
        if (selectedEntry?.kind === 'needs_me' && noteOpen[selectedEntry.item.task_id]) {
          const id = selectedEntry.item.task_id
          return setNoteOpen((o) => ({ ...o, [id]: false }))
        }
        return setLegend(false)
      }
      case 'approve':
      case 'reject': {
        if (selectedEntry?.kind !== 'needs_me') return
        const item = selectedEntry.item
        const a = itemActions(item, workflows.find((w) => w.id === item.workflow_id))
        if (action === 'approve') {
          if (a.approve && !busy[item.task_id]) void approve(item.task_id, nextAfterRemoval(flat, selectedKey))
        } else if (item.reason === 'human_gate' && a.requestChanges) {
          setNoteOpen((o) => ({ ...o, [item.task_id]: true }))
          setFocusTick((t) => t + 1)
        }
      }
    }
  }
  useInboxKeyboard(isWide && total > 0, onKey)

  const rowProps = (e: InboxEntry) =>
    isWide ? { selected: entryKey(e) === selectedKey, onSelect: () => select(entryKey(e)) } : {}

  const renderNeedsMe = (item: NeedsMeItem) => {
    const wf = workflows.find((w) => w.id === item.workflow_id)
    const a = itemActions(item, wf)
    const isGate = item.reason === 'human_gate'
    const open = !!noteOpen[item.task_id]
    const note = notes[item.task_id] ?? ''
    const noteField = !isWide && isGate && a.requestChanges && open && (
      <div className="mt-3 flex flex-col md:flex-row gap-2" data-testid="request-changes-form">
        <Field label="Request changes note" labelHidden className="flex-1">
          <textarea
            value={note}
            onChange={(e) => setNotes((n) => ({ ...n, [item.task_id]: e.target.value }))}
            placeholder="What needs to change?"
            rows={2}
            className={`${controlClasses} text-xs`}
          />
        </Field>
        <Button
          size="sm"
          variant="danger"
          disabled={busy[item.task_id] || !note.trim()}
          onClick={() => sendChanges(item.task_id)}
          className={BTN}
        >
          Send
        </Button>
      </div>
    )
    return (
      <Row key={item.task_id} to={`/tasks/${item.task_id}`} error={isWide ? undefined : errors[item.task_id]} extra={noteField || undefined} {...rowProps({ kind: 'needs_me', item })}>
        <div className="min-w-0 break-words">
          <Link to={`/tasks/${item.task_id}`} className="text-sm font-medium text-fg hover:text-fg hover:underline">
            {item.task_title}
          </Link>
          <Badge tone="accent" size="xs" className="ml-2 align-middle">
            {needsMeReasonLabel(item.reason, item.escalation_reason)}
          </Badge>
          {item.label && (
            <Badge tone="neutral" size="xs" className="ml-2 align-middle">
              {labelDisplayNameByName(item.label, wf?.labels)}
            </Badge>
          )}
          {item.message && <p className="text-xs text-fg-muted mt-1">{item.message}</p>}
          {isGate && <DecisionContext item={item} />}
          {item.reason === 'waiting_human' && (() => {
            const esc = escalationInfo(item.escalation_reason)
            return <p className="text-xs text-fg-subtle mt-1">{esc.explanation} {esc.nextStep}</p>
          })()}
        </div>
        <div className="md:shrink-0 flex flex-col items-stretch md:items-end gap-2">
          <span className="text-xs text-fg-subtle" title={new Date(item.since).toLocaleString()}>
            {formatRelativeAge(item.since)}
          </span>
          {!isWide && <div className="flex flex-wrap gap-2">
            {a.fix && (
              <Link to={a.fix.to} className={linkBtn('primary')}>{a.fix.text}</Link>
            )}
            {a.reply && <Link to={`/tasks/${item.task_id}`} className={linkBtn('primary')}>Reply</Link>}
            {a.retry && (
              <Button
                size="sm"
                variant={a.fix ? 'secondary' : 'primary'}
                disabled={busy[item.task_id]}
                onClick={() => run(item.task_id, () => api.tasks.rerun(item.task_id), refreshAll)}
                className={BTN}
              >
                Retry
              </Button>
            )}
            {a.approve && (
              <Button
                size="sm"
                variant="primary"
                disabled={busy[item.task_id]}
                onClick={() => run(item.task_id, () => api.tasks.approve(item.task_id), refreshAll)}
                className={BTN}
              >
                Approve
              </Button>
            )}
            {a.requestChanges && (
              <Button
                size="sm"
                variant="danger"
                disabled={busy[item.task_id]}
                onClick={() => setNoteOpen((o) => ({ ...o, [item.task_id]: !o[item.task_id] }))}
                className={BTN}
              >
                Request changes
              </Button>
            )}
            {!a.reply && (
              <Link to={`/tasks/${item.task_id}`} className={linkBtn('secondary')}>Open</Link>
            )}
          </div>}
        </div>
      </Row>
    )
  }

  const renderEntry = (e: InboxEntry) => {
    switch (e.kind) {
      case 'needs_me':
        return renderNeedsMe(e.item)
      case 'side_effect': {
        const { task, effects, since } = e.failed
        return (
          <Row key={`fx:${task.id}`} to={`/tasks/${task.id}`} error={isWide ? undefined : errors[`fx:${task.id}`]} {...rowProps(e)}>
            <div className="min-w-0 break-words">
              <Link to={`/tasks/${task.id}`} className="text-sm font-medium text-fg hover:text-fg hover:underline">
                {task.title}
              </Link>
              {effects.map((ef) => (
                <p key={ef.kind} className="text-xs text-danger mt-1">
                  {sideEffectText(ef)}
                  {ef.last_error ? `: ${ef.last_error}` : ''}
                </p>
              ))}
            </div>
            <div className="md:shrink-0 flex flex-col items-stretch md:items-end gap-2">
              <span className="text-xs text-fg-subtle">{formatRelativeAge(since)}</span>
              {!isWide && (
                <>
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={busy[`fx:${task.id}`]}
                    onClick={() => run(`fx:${task.id}`, () => api.tasks.dismissSideEffects(task.id), refreshEffects)}
                    className={BTN}
                  >
                    Dismiss
                  </Button>
                  <Button
                    size="sm"
                    variant="primary"
                    disabled={busy[`fx:${task.id}`]}
                    onClick={() => run(`fx:${task.id}`, () => api.tasks.retrySideEffects(task.id), refreshEffects)}
                    className={BTN}
                  >
                    Retry
                  </Button>
                </>
              )}
            </div>
          </Row>
        )
      }
      case 'source_edit': {
        const { task, since } = e.edit
        return (
          <Row key={`su:${task.id}`} to={`/tasks/${task.id}`} error={isWide ? undefined : errors[`su:${task.id}`]} {...rowProps(e)}>
            <div className="min-w-0 break-words">
              <Link to={`/tasks/${task.id}`} className="text-sm font-medium text-fg hover:text-fg hover:underline">
                {task.title}
              </Link>
              <p className="text-xs text-warning mt-1">
                The source issue was edited after approval. Review the new text before it reaches the agent.
              </p>
            </div>
            <div className="md:shrink-0 flex flex-col items-stretch md:items-end gap-2">
              <span className="text-xs text-fg-subtle">{formatRelativeAge(since)}</span>
              {!isWide && <div className="flex flex-wrap gap-2">
                <Link to={`/tasks/${task.id}`} className={linkBtn('secondary')}>Review</Link>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy[`su:${task.id}`]}
                  onClick={() => run(`su:${task.id}`, () => api.tasks.discardSourceUpdate(task.id), refreshEffects)}
                  className={BTN}
                >
                  Discard
                </Button>
                <Button
                  size="sm"
                  variant="primary"
                  disabled={busy[`su:${task.id}`]}
                  onClick={() => run(`su:${task.id}`, () => api.tasks.applySourceUpdate(task.id), refreshEffects)}
                  className={BTN}
                >
                  Apply
                </Button>
              </div>}
            </div>
          </Row>
        )
      }
      case 'provider': {
        const p = e.provider
        return (
          <Row key={`pv:${p.provider}`} error={isWide ? undefined : errors[`pv:${p.provider}`]} {...rowProps(e)}>
            <div className="min-w-0 break-words">
              <p className="text-sm font-medium text-fg">
                {providerLabel(p.provider)} dispatch is paused
              </p>
              <p className="text-xs text-fg-muted mt-1">
                {p.reason || 'The provider failed to authenticate.'} Queued tasks wait until it resumes.{' '}
                <Link to="/settings/health" className="underline">View health</Link>
              </p>
            </div>
            <div className="md:shrink-0 flex flex-col items-stretch md:items-end gap-2">
              <span className="text-xs text-fg-subtle">{formatRelativeAge(p.paused_at)}</span>
              {!isWide && (
                <Button
                  size="sm"
                  variant="primary"
                  disabled={busy[`pv:${p.provider}`]}
                  onClick={() => run(`pv:${p.provider}`, () => resume(p.provider), () => undefined)}
                  className={BTN}
                >
                  Resume
                </Button>
              )}
            </div>
          </Row>
        )
      }
    }
  }

  const previewNode = (() => {
    const e = selectedEntry
    const base = {
      entry: e,
      noteRef,
      replyRef,
    }
    let ekey = ''
    let id = ''
    let item: NeedsMeItem | undefined
    if (e) {
      switch (e.kind) {
        case 'needs_me': ekey = id = e.item.task_id; item = e.item; break
        case 'source_edit': id = e.edit.task.id; ekey = `su:${id}`; break
        case 'side_effect': id = e.failed.task.id; ekey = `fx:${id}`; break
        case 'provider': ekey = `pv:${e.provider.provider}`; break
      }
    }
    const adv = nextAfterRemoval(flat, selectedKey)
    return (
      <InboxPreview
        {...base}
        workflow={item ? workflows.find((w) => w.id === item.workflow_id) : undefined}
        busy={!!busy[ekey]}
        error={errors[ekey]}
        noteOpen={!!noteOpen[id]}
        note={notes[id] ?? ''}
        setNote={(v) => setNotes((n) => ({ ...n, [id]: v }))}
        setNoteOpen={(v) => setNoteOpen((o) => ({ ...o, [id]: v }))}
        reply={replies[id] ?? ''}
        setReply={(v) => setReplies((r) => ({ ...r, [id]: v }))}
        onApprove={() => void approve(id, adv)}
        onReject={() => void sendChanges(id, adv)}
        onReply={() => item && void sendReply(item, adv)}
        onRetry={() => void run(id, () => api.tasks.rerun(id), refreshAll, adv)}
        onDismissEffects={() => void run(ekey, () => api.tasks.dismissSideEffects(id), refreshEffects, adv)}
        onRetryEffects={() => void run(ekey, () => api.tasks.retrySideEffects(id), refreshEffects, adv)}
        onApplySource={() => void run(ekey, () => api.tasks.applySourceUpdate(id), refreshEffects, adv)}
        onDiscardSource={() => void run(ekey, () => api.tasks.discardSourceUpdate(id), refreshEffects, adv)}
        onResume={() => e?.kind === 'provider' && void run(ekey, () => resume(e.provider.provider), () => undefined, adv)}
      />
    )
  })()

  const sectionsNode = SECTION_ORDER.map((k) => (
    <Section key={k} id={k} count={sections[k].length}>{sections[k].map(renderEntry)}</Section>
  ))

  return (
    <PageFrame
      title="Inbox"
      width={isWide ? 'wide' : 'default'}
      testId="inbox-page"
      titleAdornment={
        <>
          <span data-testid="inbox-total" className="text-sm text-fg-muted">({total})</span>
          {isWide && (
            <button
              aria-label="Keyboard shortcuts"
              data-testid="inbox-shortcuts-toggle"
              aria-expanded={legend}
              onClick={() => setLegend((v) => !v)}
              className="ml-1 w-5 h-5 text-xs rounded-full border border-border text-fg-muted hover:text-fg"
            >
              ?
            </button>
          )}
        </>
      }
    >

      {isWide && legend && (
        <p data-testid="inbox-shortcuts" className="text-xs text-fg-muted mb-4 -mt-3">
          <kbd>j</kbd>/<kbd>k</kbd> or ↑/↓ move · <kbd>a</kbd> approve · <kbd>r</kbd> request changes · <kbd>Enter</kbd>/<kbd>o</kbd> open full page · <kbd>Esc</kbd> close note/legend · <kbd>?</kbd> this help
        </p>
      )}

      {total === 0 && (
        <div data-testid="inbox-empty" className="text-center text-fg-subtle py-16">
          <p className="text-sm text-fg">You&apos;re all caught up.</p>
          <p className="text-xs mt-1">Nothing is waiting on you. New items appear here as agents need you.</p>
        </div>
      )}

      {isWide && total > 0 ? (
        <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-6">
          <div>{sectionsNode}</div>
          <aside className="sticky top-4 self-start max-h-[calc(100vh-2rem)] overflow-y-auto">{previewNode}</aside>
        </div>
      ) : (
        sectionsNode
      )}
    </PageFrame>
  )
}
