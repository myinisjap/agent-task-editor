import { useEffect, useState, type ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import type { AgentConfig, AgentRun, ProviderConfig, Task } from '../../api/client'
import type { DiffComment } from '../../lib/diffComments'
import HelpButton from '../shared/HelpButton'
import DiffReviewPane from './DiffReviewPane'
import ReviewBriefCard from './ReviewBriefCard'
import RunHistoryList from './RunHistoryList'
import RunLogPane from './RunLogPane'
import VerificationPanel from './VerificationPanel'
import { rejectDisabled, rejectPlaceholder } from './TaskActions'

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
  verificationKey, briefKey, mode, onBack, onHelp, details, variant = 'desktop',
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
    <p className="text-xs text-amber-400">
      💬 {openCommentsCount} open diff comment{openCommentsCount !== 1 ? 's' : ''} — the next run will see them
    </p>
  )
  const errorNote = actionError && <p role="alert" className="text-xs text-red-400">{actionError}</p>

  const header = phone ? (
    <div data-testid="review-header" className="shrink-0 border-b border-slate-800 px-3 py-2 flex items-start justify-between gap-2">
      <div className="min-w-0">
        <div className="text-xs text-slate-500 mb-0.5">
          <button onClick={onBack} className="hover:text-slate-300">← Board</button>
        </div>
        <h1 className="text-base font-semibold text-slate-100 break-words">{task.title}</h1>
        <div className="mt-1">
          <span data-testid="review-state-chip" className="px-2 py-0.5 text-xs rounded-full bg-amber-900/40 border border-amber-700 text-amber-300 font-medium">
            Waiting for your review
          </span>
        </div>
      </div>
      {onHelp && <HelpButton onClick={onHelp} title="About this task" />}
    </div>
  ) : (
    <div data-testid="review-header" className="shrink-0 border-b border-slate-800 px-5 py-3 flex items-start justify-between gap-4">
      <div className="min-w-0">
        <div className="text-xs text-slate-500 mb-1">
          <button onClick={onBack} className="hover:text-slate-300">← Board</button>
          {repoName ? ` / ${repoName}` : ''} / {task.label}
        </div>
        <h1 className="text-lg font-semibold text-slate-100 break-words">{task.title}</h1>
        <div className="flex flex-wrap items-center gap-3 mt-1 text-xs text-slate-400">
          <span data-testid="review-state-chip" className="px-2 py-0.5 rounded-full bg-amber-900/40 border border-amber-700 text-amber-300 font-medium">
            Waiting for your review
          </span>
          {repoName && <span>{repoName}</span>}
          <span>${(task.cumulative_cost_usd ?? 0).toFixed(2)}</span>
          <span>{runs.length} run{runs.length === 1 ? '' : 's'}</span>
        </div>
      </div>
      <div className="shrink-0 flex flex-col items-end gap-2">
        <div className="flex items-center gap-2">
          {onHelp && <HelpButton onClick={onHelp} title="About this task" />}
          {canReject && (
            <button
              onClick={() => setShowRejectForm((s) => !s)}
              className="px-4 py-1.5 text-xs font-medium rounded border border-red-700 text-red-300 hover:bg-red-900/30"
            >
              Request changes
            </button>
          )}
          {canApprove && (
            <button
              onClick={onApprove}
              disabled={actionPending}
              className="px-4 py-1.5 text-xs font-medium rounded bg-emerald-600 hover:bg-emerald-500 text-white disabled:opacity-50"
            >
              {approveLabel}
            </button>
          )}
        </div>
        {commentsNote}
        {errorNote}
      </div>
    </div>
  )

  const rejectForm = (
    <div className={phone ? 'flex flex-col gap-2' : 'shrink-0 border-b border-slate-800 bg-slate-900 px-5 py-3 flex gap-3 items-start'}>
      <textarea
        value={rejectNote}
        onChange={(e) => setRejectNote(e.target.value)}
        placeholder={rejectPlaceholder(openCommentsCount)}
        rows={3}
        className={`flex-1 min-w-0 bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500 ${phone ? 'text-base w-full' : 'text-xs'}`}
      />
      <div className={phone ? 'flex gap-2 [&>button]:flex-1' : 'flex flex-col gap-2'}>
        <button
          onClick={onReject}
          disabled={rejectDisabled(rejectNote, openCommentsCount, actionPending)}
          className={`px-4 py-1.5 text-xs font-medium rounded bg-red-700 hover:bg-red-600 text-white disabled:opacity-50 ${phone ? 'min-h-[44px]' : ''}`}
        >
          Send back with changes
        </button>
        <button
          onClick={() => setShowRejectForm(false)}
          className={`px-4 py-1.5 text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-100 ${phone ? 'min-h-[44px]' : ''}`}
        >
          Cancel
        </button>
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
    <div className={stacked ? 'flex flex-col min-w-0' : 'col-span-2 flex flex-col min-h-0 border-r border-slate-800'}>
      <div className={`shrink-0 flex items-center gap-2 border-b border-slate-800 px-4 pt-2 ${stacked ? 'overflow-x-auto' : ''}`}>
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`px-3 py-1.5 text-xs font-medium rounded-t transition-colors ${
              tab === t.id ? 'bg-slate-800 text-slate-100 border-b-2 border-slate-400' : 'text-slate-500 hover:text-slate-300'
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
              <details className="rounded border border-slate-800 bg-slate-900/60 p-3">
                <summary className="cursor-pointer text-xs font-semibold text-slate-400">Task description</summary>
                <p className="mt-2 text-sm text-slate-300 whitespace-pre-wrap">{task.description}</p>
              </details>
            )}
            {task.agent_notes ? (
              <div className="text-sm text-slate-200 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:font-semibold [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-2 [&_code]:font-mono [&_pre]:overflow-x-auto break-words">
                <ReactMarkdown>{task.agent_notes}</ReactMarkdown>
              </div>
            ) : (
              <p className="text-sm text-slate-500">No plan notes recorded.</p>
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
        {tab === 'logs' && (
          stacked
            ? <div className="h-[70vh]"><RunLogPane taskId={task.id} runId={selectedRun} isRunning={isRunning} /></div>
            : <RunLogPane taskId={task.id} runId={selectedRun} isRunning={isRunning} />
        )}
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
        className="shrink-0 border-t border-slate-700 bg-slate-900 p-3 flex flex-col gap-2 max-md:mb-[var(--ate-bottom-bar-height,0px)] max-h-[55vh] overflow-y-auto"
      >
        {commentsNote}
        {errorNote}
        {showRejectForm && canReject && rejectForm}
        <div className="flex gap-2 [&>button]:flex-1">
          {canReject && (
            <button
              onClick={() => setShowRejectForm((s) => !s)}
              className="min-h-[44px] px-3 text-sm font-medium rounded border border-red-700 text-red-300 hover:bg-red-900/30"
            >
              Request changes
            </button>
          )}
          {canApprove && (
            <button
              onClick={onApprove}
              disabled={actionPending}
              className="min-h-[44px] px-3 text-sm font-medium rounded bg-emerald-600 hover:bg-emerald-500 text-white disabled:opacity-50"
            >
              {approveLabel}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
