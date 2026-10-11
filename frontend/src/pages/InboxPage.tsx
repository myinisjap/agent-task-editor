import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api/client'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { useWorkflowStore } from '../stores/workflow'
import { buildInbox, inboxCount, itemActions } from '../lib/inbox'
import { needsMeReasonLabel } from '../lib/needsMe'
import { formatRelativeAge } from '../lib/format'
import { sideEffectText } from '../components/shared/SideEffectBadge'
import { providerLabel } from '../components/shared/ProviderPausedBanner'
import type { NeedsMeItem } from '../api/client'

const BTN = 'px-3 py-1.5 text-xs font-medium rounded text-white disabled:opacity-50'
const PRIMARY = `${BTN} bg-sky-600 hover:bg-sky-500`
const SECONDARY = `${BTN} bg-slate-700 hover:bg-slate-600 text-slate-100`

function Section({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  if (count === 0) return null
  return (
    <section className="mb-8" data-testid={`inbox-section-${title}`}>
      <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
        {title} ({count})
      </h2>
      <div className="flex flex-col gap-3">{children}</div>
    </section>
  )
}

function Row({ children, error }: { children: ReactNode; error?: string }) {
  return (
    <div className="bg-slate-900 border border-slate-700 rounded-lg p-4" data-testid="inbox-item">
      <div className="flex items-start justify-between gap-4">{children}</div>
      {error && <p role="alert" className="text-xs text-red-400 mt-2">{error}</p>}
    </div>
  )
}

export default function InboxPage() {
  const needsMe = useNeedsMeStore((s) => s.items)
  const refreshNeedsMe = useNeedsMeStore((s) => s.refresh)
  const failed = useInboxStore((s) => s.failedEffects)
  const sourceEdits = useInboxStore((s) => s.sourceEdits)
  const refreshEffects = useInboxStore((s) => s.refresh)
  const paused = useHealthStore((s) => s.paused)
  const resume = useHealthStore((s) => s.resume)
  const fetchHealth = useHealthStore((s) => s.fetch)
  const workflows = useWorkflowStore((s) => s.workflows)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})

  useEffect(() => {
    void refreshNeedsMe()
    void refreshEffects()
    void fetchHealth()
    if (useWorkflowStore.getState().workflows.length === 0) void useWorkflowStore.getState().fetch()
  }, [refreshNeedsMe, refreshEffects, fetchHealth])

  const groups = buildInbox(needsMe, failed, paused, sourceEdits)
  const total = inboxCount(groups)

  const run = async (key: string, fn: () => Promise<unknown>, after: () => Promise<unknown> | void) => {
    setErrors((e) => ({ ...e, [key]: '' }))
    setBusy((b) => ({ ...b, [key]: true }))
    try {
      await fn()
      await after()
    } catch (e) {
      setErrors((m) => ({ ...m, [key]: e instanceof Error ? e.message : String(e) }))
    } finally {
      setBusy((b) => ({ ...b, [key]: false }))
    }
  }

  const refreshAll = () => Promise.all([refreshNeedsMe(), refreshEffects()])

  const renderNeedsMe = (item: NeedsMeItem) => {
    const a = itemActions(item, workflows.find((w) => w.id === item.workflow_id))
    return (
      <Row key={item.task_id} error={errors[item.task_id]}>
        <div className="min-w-0">
          <Link to={`/tasks/${item.task_id}`} className="text-sm font-medium text-slate-200 hover:text-white hover:underline">
            {item.task_title}
          </Link>
          <span className="ml-2 align-middle text-[10px] px-1.5 py-0.5 rounded bg-pink-900/60 text-pink-200 font-medium">
            {needsMeReasonLabel(item.reason, item.escalation_reason)}
          </span>
          {item.message && <p className="text-xs text-slate-400 mt-1">{item.message}</p>}
        </div>
        <div className="shrink-0 flex flex-col items-end gap-2">
          <span className="text-xs text-slate-500" title={new Date(item.since).toLocaleString()}>
            {formatRelativeAge(item.since)}
          </span>
          <div className="flex gap-2">
            {a.fix && (
              <Link to={a.fix.to} className={PRIMARY}>{a.fix.text}</Link>
            )}
            {a.reply && <Link to={`/tasks/${item.task_id}`} className={PRIMARY}>Reply</Link>}
            {a.retry && (
              <button
                disabled={busy[item.task_id]}
                onClick={() => run(item.task_id, () => api.tasks.rerun(item.task_id), refreshAll)}
                className={a.fix ? SECONDARY : PRIMARY}
              >
                Retry
              </button>
            )}
            {a.approve && (
              <button
                disabled={busy[item.task_id]}
                onClick={() => run(item.task_id, () => api.tasks.approve(item.task_id), refreshAll)}
                className={`${BTN} bg-emerald-600 hover:bg-emerald-500`}
              >
                Approve
              </button>
            )}
            {!a.reply && (
              <Link to={`/tasks/${item.task_id}`} className={SECONDARY}>Open</Link>
            )}
          </div>
        </div>
      </Row>
    )
  }

  return (
    <div className="p-6 max-w-5xl mx-auto" data-testid="inbox-page">
      <h1 className="text-xl font-semibold text-slate-100 mb-6">Inbox</h1>

      {total === 0 && (
        <div data-testid="inbox-empty" className="text-center text-slate-500 py-16">
          <p className="text-sm text-slate-300">You&apos;re all caught up.</p>
          <p className="text-xs mt-1">Nothing is waiting on you. New items appear here as agents need you.</p>
        </div>
      )}

      <Section title="Needs a reply" count={groups.reply.length}>{groups.reply.map(renderNeedsMe)}</Section>
      <Section title="Awaiting approval" count={groups.approval.length}>{groups.approval.map(renderNeedsMe)}</Section>
      <Section title="PR conflicts" count={groups.pr_conflict.length}>{groups.pr_conflict.map(renderNeedsMe)}</Section>

      <Section title="Failed side effects" count={groups.side_effect.length}>
        {groups.side_effect.map(({ task, effects, since }) => (
          <Row key={task.id} error={errors[`fx:${task.id}`]}>
            <div className="min-w-0">
              <Link to={`/tasks/${task.id}`} className="text-sm font-medium text-slate-200 hover:text-white hover:underline">
                {task.title}
              </Link>
              {effects.map((e) => (
                <p key={e.kind} className="text-xs text-red-300 mt-1">
                  {sideEffectText(e)}
                  {e.last_error ? `: ${e.last_error}` : ''}
                </p>
              ))}
            </div>
            <div className="shrink-0 flex flex-col items-end gap-2">
              <span className="text-xs text-slate-500">{formatRelativeAge(since)}</span>
              <button
                disabled={busy[`fx:${task.id}`]}
                onClick={() => run(`fx:${task.id}`, () => api.tasks.retrySideEffects(task.id), refreshEffects)}
                className={PRIMARY}
              >
                Retry
              </button>
            </div>
          </Row>
        ))}
      </Section>

      <Section title="Source issue edited" count={groups.source_edit.length}>
        {groups.source_edit.map(({ task, since }) => (
          <Row key={task.id} error={errors[`su:${task.id}`]}>
            <div className="min-w-0">
              <Link to={`/tasks/${task.id}`} className="text-sm font-medium text-slate-200 hover:text-white hover:underline">
                {task.title}
              </Link>
              <p className="text-xs text-amber-300 mt-1">
                The source issue was edited after approval. Review the new text before it reaches the agent.
              </p>
            </div>
            <div className="shrink-0 flex flex-col items-end gap-2">
              <span className="text-xs text-slate-500">{formatRelativeAge(since)}</span>
              <div className="flex gap-2">
                <Link to={`/tasks/${task.id}`} className={SECONDARY}>Review</Link>
                <button
                  disabled={busy[`su:${task.id}`]}
                  onClick={() => run(`su:${task.id}`, () => api.tasks.discardSourceUpdate(task.id), refreshEffects)}
                  className={SECONDARY}
                >
                  Discard
                </button>
                <button
                  disabled={busy[`su:${task.id}`]}
                  onClick={() => run(`su:${task.id}`, () => api.tasks.applySourceUpdate(task.id), refreshEffects)}
                  className={PRIMARY}
                >
                  Apply
                </button>
              </div>
            </div>
          </Row>
        ))}
      </Section>

      <Section title="Paused providers" count={groups.provider.length}>
        {groups.provider.map((p) => (
          <Row key={p.provider} error={errors[`pv:${p.provider}`]}>
            <div className="min-w-0">
              <p className="text-sm font-medium text-slate-200">
                {providerLabel(p.provider)} dispatch is paused
              </p>
              <p className="text-xs text-slate-400 mt-1">
                {p.reason || 'The provider failed to authenticate.'} Queued tasks wait until it resumes.{' '}
                <Link to="/health" className="underline">View health</Link>
              </p>
            </div>
            <div className="shrink-0 flex flex-col items-end gap-2">
              <span className="text-xs text-slate-500">{formatRelativeAge(p.paused_at)}</span>
              <button
                disabled={busy[`pv:${p.provider}`]}
                onClick={() => run(`pv:${p.provider}`, () => resume(p.provider), () => undefined)}
                className={PRIMARY}
              >
                Resume
              </button>
            </div>
          </Row>
        ))}
      </Section>
    </div>
  )
}
