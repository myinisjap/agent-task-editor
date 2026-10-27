import { MessageSquare } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import type { AgentConfig, AgentRun, ProviderConfig, Task, TaskLabelHistoryEntry } from '../../api/client'
import type { DiffComment } from '../../lib/diffComments'
import HelpButton from '../shared/HelpButton'
import DiffReviewPane from './DiffReviewPane'
import ReviewBriefCard from './ReviewBriefCard'
import RunHistoryList from './RunHistoryList'
import RunLogPane from './RunLogPane'
import VerificationPanel from './VerificationPanel'
import { rejectDisabled, rejectPlaceholder } from './TaskActions'
import { Badge, Button, Field, controlClasses, Icon } from '../ui'

type MainTab = 'plan' | 'diff' | 'logs' | 'details'

// ReviewLayout is the desktop (≥1280px) decision-first screen for a task
// sitting at a human gate: header with the decision buttons, diff/plan in the
// main pane, and a sticky rail with brief, verification and runs. It is purely
// presentational — TaskDetailPage owns all state and handlers.
export default function ReviewLayout({
  task, repoName, runs, agentConfigs, providerConfigs, selectedRun, onSelectRun,
  isRunning, canRerun, onStop, onRerun, canApprove, canReject, approveLabel, onApprove,
  rejectNote, setRejectNote, onReject, openCommentsCount, actionError, actionPending,
  diffComments, onAddComment, onRemoveComment, onReopenComment, onCreatePR, creatingPR,
  verificationKey, briefKey, mode, onBack, onHelp, details, labelHistory, variant = 'desktop',
}: {
  task: Task
  repoName?: string
  runs: AgentRun[]
  agentConfigs: AgentConfig[]
  providerConfigs: ProviderConfig[]
  selectedRun: string | null
  onSelectRun: (runId: string) => void
  isRunning: boolean
  canRerun: boolean
  onStop: () => void
  onRerun: () => void
  canApprove: boolean
  canReject: boolean
  approveLabel: string
  onApprove: () => void
  rejectNote: string
  setRejectNote: (v: string) => void
  onReject: () => void
  openCommentsCount: number
  actionError?: string
  actionPending: boolean
  diffComments: DiffComment[]
  onAddComment: (draft: DiffComment) => void
  onRemoveComment: (commentId: string) => void
  onReopenComment: (commentId: string) => void
  onCreatePR: () => void
  creatingPR: boolean
  verificationKey: number
  briefKey: string
  mode: 'diff' | 'plan'
  onBack: () => void
  onHelp?: () => void
  details: ReactNode
  labelHistory?: TaskLabelHistoryEntry[]
  variant?: 'desktop' | 'tablet' | 'phone'
}) {
  const defaultTab: MainTab = mode === 'plan' ? 'plan' : 'diff'
  const [tab, setTab] = useState<MainTab>(defaultTab)
  const [showRejectForm, setShowRejectForm] = useState(false)

  useEffect(() => { setTab(defaultTab) }, [defaultTab])
  // A successful decision moves the task; collapse the form.
  useEffect(() => { setShowRejectForm(false) }, [task.label])

  const tabs: { id: MainTab; label: string }[] =
    mode === 'plan'
      ? [{ id: 'plan', label: 'Plan' }, { id: 'diff', label: 'Diff' }, { id: 'logs', label: 'Logs' }, { id: 'details', label: 'Details' }]
      : [{ id: 'diff', label: 'Diff' }, { id: 'logs', label: 'Logs' }, { id: 'details', label: 'Details' }]

  const selectRun = (runId: string) => {
    onSelectRun(runId)
    setTab('logs')
  }

  const runsList = (
    <RunHistoryList
      runs={runs}
      agentConfigs={agentConfigs}
      providerConfigs={providerConfigs}
      selectedRun={selectedRun}
      onSelectRun={selectRun}
      isRunning={isRunning}
      canRerun={canRerun}
      onStop={onStop}
      onRerun={onRerun}
      actionPending={actionPending}
    />
  )

  const phone = variant === 'phone'
  const stacked = variant !== 'desktop'

  const commentsNote = openCommentsCount > 0 && (
    <p className="text-xs text-warning">
      <Icon icon={MessageSquare} size="sm" className="inline -mt-0.5 mr-1" />{openCommentsCount} open diff comment{openCommentsCount !== 1 ? 's' : ''} — the next run will see them
    </p>
  )
  const errorNote = actionError && <p role="alert" className="text-xs text-danger">{actionError}</p>

  const header = phone ? (
    <div data-testid="review-header" className="shrink-0 border-b border-border px-3 py-2 flex items-start justify-between gap-2">
      <div className="min-w-0">
        <div className="text-xs text-fg-subtle mb-0.5">
          <button onClick={onBack} className="hover:text-fg">← Board</button>
        </div>
        <h1 className="text-base font-semibold text-fg break-words">{task.title}</h1>
        <div className="mt-1">
          <Badge tone="warning" data-testid="review-state-chip">
            Waiting for your review
          </Badge>
        </div>
      </div>
      {onHelp && <HelpButton onClick={onHelp} title="About this task" />}
    </div>
  ) : (
    <div data-testid="review-header" className="shrink-0 border-b border-border px-5 py-3 flex items-start justify-between gap-4">
      <div className="min-w-0">
        <div className="text-xs text-fg-subtle mb-1">
          <button onClick={onBack} className="hover:text-fg">← Board</button>
          {repoName ? ` / ${repoName}` : ''} / {task.label}
        </div>
        <h1 className="text-lg font-semibold text-fg break-words">{task.title}</h1>
        <div className="flex flex-wrap items-center gap-3 mt-1 text-xs text-fg-muted">
          <Badge tone="warning" data-testid="review-state-chip">
            Waiting for your review
          </Badge>
          {repoName && <span>{repoName}</span>}
          <span>${(task.cumulative_cost_usd ?? 0).toFixed(2)}</span>
          <span>{runs.length} run{runs.length === 1 ? '' : 's'}</span>
        </div>
      </div>
      <div className="shrink-0 flex flex-col items-end gap-2">
        <div className="flex items-center gap-2">
          {onHelp && <HelpButton onClick={onHelp} title="About this task" />}
          {canReject && (
            <Button size="sm" variant="danger" onClick={() => setShowRejectForm((s) => !s)}>
              Request changes
            </Button>
          )}
          {canApprove && (
            <Button size="sm" variant="primary" onClick={onApprove} disabled={actionPending}>
              {approveLabel}
            </Button>
          )}
        </div>
        {commentsNote}
        {errorNote}
      </div>
    </div>
  )

  const rejectForm = (
    <div className={phone ? 'flex flex-col gap-2' : 'shrink-0 border-b border-border bg-surface-raised px-5 py-3 flex gap-3 items-start'}>
      <Field label="Rejection note" labelHidden className="flex-1 min-w-0">
        <textarea
          value={rejectNote}
          onChange={(e) => setRejectNote(e.target.value)}
          placeholder={rejectPlaceholder(openCommentsCount)}
          rows={3}
          className={`${controlClasses} resize-none ${phone ? 'text-base' : 'text-xs'}`}
        />
      </Field>
      <div className={phone ? 'flex gap-2 [&>button]:flex-1' : 'flex flex-col gap-2'}>
        <Button
          size="sm"
          variant="danger"
          onClick={onReject}
          disabled={rejectDisabled(rejectNote, openCommentsCount, actionPending)}
          className={phone ? 'min-h-[44px]' : ''}
        >
          Send back with changes
        </Button>
        <Button size="sm" variant="secondary" onClick={() => setShowRejectForm(false)} className={phone ? 'min-h-[44px]' : ''}>
          Cancel
        </Button>
      </div>
    </div>
  )

  const rail = (
    <>
      <ReviewBriefCard taskId={task.id} refreshKey={briefKey} hidePreview={mode === 'diff'} />
      <VerificationPanel taskId={task.id} refreshKey={verificationKey} />
      {runsList}
    </>
  )

  const detailsPane = phone ? (
    <>
      <VerificationPanel taskId={task.id} refreshKey={verificationKey} />
      {runsList}
      {details}
    </>
  ) : details

  const paneCls = stacked ? '' : 'h-full overflow-y-auto'
  const mainTabs = (
    <div className={stacked ? 'flex flex-col min-w-0' : 'col-span-2 flex flex-col min-h-0 border-r border-border'}>
      <div className={`shrink-0 flex items-center gap-2 border-b border-border px-4 pt-2 ${stacked ? 'overflow-x-auto' : ''}`}>
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`px-3 py-1.5 text-xs font-medium rounded-t transition-colors ${
              tab === t.id ? 'bg-surface-raised text-accent-fg border-b-2 border-accent' : 'text-fg-subtle hover:text-fg'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div className={stacked ? 'min-w-0' : 'flex-1 min-h-0 overflow-hidden'}>
        {tab === 'plan' && (
          <div data-testid="review-plan-pane" className={`${paneCls} p-5 flex flex-col gap-4`}>
            {task.description && (
              <details className="rounded border border-border bg-surface-raised/60 p-3">
                <summary className="cursor-pointer text-xs font-semibold text-fg-muted">Task description</summary>
                <p className="mt-2 text-sm text-fg whitespace-pre-wrap">{task.description}</p>
              </details>
            )}
            {task.agent_notes ? (
              <div className="text-sm text-fg [&_h1]:text-base [&_h1]:font-semibold [&_h2]:font-semibold [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-2 [&_code]:font-mono [&_pre]:overflow-x-auto break-words">
                <ReactMarkdown>{task.agent_notes}</ReactMarkdown>
              </div>
            ) : (
              <p className="text-sm text-fg-subtle">No plan notes recorded.</p>
            )}
          </div>
        )}
        {tab === 'diff' && (
          <DiffReviewPane
            taskId={task.id}
            prUrl={task.pr_url}
            onCreatePR={onCreatePR}
            creatingPR={creatingPR}
            diffComments={diffComments}
            onAddComment={onAddComment}
            onRemoveComment={onRemoveComment}
            onReopenComment={onReopenComment}
            showBrief={false}
            hideVerification
            showPreview
            previewRefreshKey={briefKey}
          />
        )}
        {tab === 'logs' && (() => {
          const pane = (
            <RunLogPane
              taskId={task.id}
              runId={selectedRun}
              isRunning={isRunning}
              run={runs.find((r) => r.id === selectedRun)}
              agentConfigs={agentConfigs}
              providerConfigs={providerConfigs}
              labelHistory={labelHistory}
              onRetry={canRerun ? onRerun : undefined}
              onOpenDiff={() => setTab('diff')}
            />
          )
          return stacked ? <div className="h-[70vh]">{pane}</div> : pane
        })()}
        {tab === 'details' && (
          <div className={`${stacked ? '' : 'h-full overflow-y-auto'} overflow-x-hidden p-5 flex flex-col gap-4 min-w-0`}>{detailsPane}</div>
        )}
      </div>
    </div>
  )

  if (variant === 'desktop') {
    return (
      <div data-testid="review-layout" data-variant={variant} className="flex flex-col h-full overflow-hidden min-w-0">
        {header}
        {showRejectForm && canReject && rejectForm}
        <div className="flex-1 min-h-0 grid grid-cols-3">
          {mainTabs}
          <div data-testid="review-rail" className="col-span-1 min-h-0 overflow-y-auto p-4 flex flex-col gap-4 min-w-0">
            {rail}
          </div>
        </div>
      </div>
    )
  }

  if (variant === 'tablet') {
    return (
      <div data-testid="review-layout" data-variant={variant} className="flex flex-col h-full overflow-hidden min-w-0">
        {header}
        {showRejectForm && canReject && rejectForm}
        <div className="flex-1 min-h-0 overflow-y-auto">
          <div data-testid="review-rail" className="p-4 flex flex-col gap-4 min-w-0">
            {rail}
          </div>
          {mainTabs}
        </div>
      </div>
    )
  }

  return (
    <div data-testid="review-layout" data-variant={variant} className="flex flex-col h-full overflow-hidden min-w-0">
      {header}
      <div className="flex-1 min-h-0 overflow-y-auto">
        <div className="p-3">
          <ReviewBriefCard taskId={task.id} refreshKey={briefKey} compact />
        </div>
        {mainTabs}
      </div>
      <div
        data-testid="review-action-bar"
        className="shrink-0 border-t border-border bg-surface-raised p-3 flex flex-col gap-2 max-md:mb-[var(--ate-bottom-bar-height,0px)] max-h-[55vh] overflow-y-auto"
      >
        {commentsNote}
        {errorNote}
        {showRejectForm && canReject && rejectForm}
        <div className="flex gap-2 [&>button]:flex-1">
          {canReject && (
            <Button variant="danger" onClick={() => setShowRejectForm((s) => !s)} className="min-h-[44px]">
              Request changes
            </Button>
          )}
          {canApprove && (
            <Button variant="primary" onClick={onApprove} disabled={actionPending} className="min-h-[44px]">
              {approveLabel}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
