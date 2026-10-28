import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { PageFrame } from '../components/ui'
import { useDashboard } from '../lib/useDashboard'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore, useInboxSections } from '../stores/inbox'
import { inboxCount } from '../lib/inbox'
import { useNeedsYouFlash } from '../lib/useNeedsYouFlash'
import { useWakeLock } from '../lib/useWakeLock'
import { useHomeTicker } from '../lib/useHomeTicker'
import { useRunSteps } from '../lib/useRunSteps'
import RecentTicker from '../components/home/RecentTicker'
import { useHealthStore } from '../stores/health'
import { useWorkflowStore } from '../stores/workflow'
import { useTasksStore } from '../stores/tasks'
import { useIsPhone, useMediaQuery } from '../lib/useMediaQuery'
import { entryHref } from '../lib/homeInbox'
import type { InboxEntry } from '../lib/inbox'
import HelpModal from '../components/shared/HelpModal'
import { HomeHelp } from '../components/shared/pageHelp'
import SceneBand from '../components/home/SceneBand'
import NeedsYouPanel from '../components/home/NeedsYouPanel'
import RunningNowPanel from '../components/home/RunningNowPanel'
import TodayPanel from '../components/home/TodayPanel'
import DispatchPill from '../components/home/DispatchPill'

export default function HomePage() {
  const navigate = useNavigate()
  const { dash } = useDashboard()
  const sections = useInboxSections()
  const refreshNeedsMe = useNeedsMeStore((st) => st.refresh)
  const refreshEffects = useInboxStore((st) => st.refresh)
  const fetchHealth = useHealthStore((st) => st.fetch)
  const paused = useHealthStore((st) => st.paused)
  const workflows = useWorkflowStore((s) => s.workflows)
  const workflow = useWorkflowStore((s) => s.active())
  const tasks = useTasksStore((s) => s.tasks)
  const isPhone = useIsPhone()
  const [showHelp, setShowHelp] = useState(false)
  const reduceMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const needsMeLoaded = useNeedsMeStore((s) => s.loaded)
  const effectsLoaded = useInboxStore((s) => s.loaded)
  const { flashing, flashKey } = useNeedsYouFlash(inboxCount(sections), needsMeLoaded && effectsLoaded)
  useWakeLock()
  const needsItems = useNeedsMeStore((s) => s.items)
  const ticker = useHomeTicker({
    labels: workflow?.labels,
    titleFor: (id) =>
      tasks.find((t) => t.id === id)?.title ??
      dash?.active_agents.find((a) => a.task_id === id)?.task_title ??
      needsItems.find((n) => n.task_id === id)?.task_title,
  })
  const steps = useRunSteps(dash?.active_agents ?? [])

  // Make sure every input to the shared count is fresh (the nav syncs also keep them live).
  useEffect(() => {
    void refreshNeedsMe()
    void refreshEffects()
    void fetchHealth()
  }, [refreshNeedsMe, refreshEffects, fetchHealth])

  useEffect(() => {
    if (workflows.length === 0) useWorkflowStore.getState().fetch()
  }, [workflows.length])

  // The single row-activation hook; the Inbox popup task swaps this one function.
  const onSelect = (e: InboxEntry) => navigate(entryHref(e))

  // MAX_WORKERS isn't in GET /dashboard; the best available figure is the
  // largest repo_concurrency limit (a repo's cap, else the global MAX_WORKERS).
  // TODO: a `max_workers` dashboard field would make this exact.
  const limits = dash?.repo_concurrency.map((r) => r.limit) ?? []
  const maxWorkers = limits.length > 0 ? Math.max(...limits) : undefined
  const counts = dash && workflow ? (dash.workflow_label_counts?.[workflow.id] ?? dash.label_counts) : undefined
  const budget = dash?.global_cost_budget

  const scene = workflow && counts ? <SceneBand workflow={workflow} labelCounts={counts} /> : null
  const needsYou = <NeedsYouPanel sections={sections} onSelect={onSelect} highlight={flashing} limit={isPhone ? 2 : undefined} />
  const running = dash ? (
    <RunningNowPanel
      agents={dash.active_agents}
      maxWorkers={maxWorkers}
      repoConcurrency={dash.repo_concurrency}
      workflow={workflow}
      tasks={tasks}
      steps={steps}
    />
  ) : null
  const today = dash ? <TodayPanel dash={dash} /> : null

  return (
    <PageFrame
      width="wide"
      headerClassName="mb-3"
      title="Home"
      testId="home-page"
      help={{ onClick: () => setShowHelp(true), title: 'About this page' }}
      actions={
        <div className="flex items-center gap-3 min-w-0">
          <DispatchPill paused={paused} budgetTripped={budget?.tripped} budgetReason={budget?.tripped_reason} />
          {dash && (
            <span data-testid="workers" className="text-xs text-fg-muted whitespace-nowrap">
              Workers {dash.active_agents.length}{maxWorkers !== undefined ? ` / ${maxWorkers}` : ''}
            </span>
          )}
        </div>
      }
    >
      {showHelp && (
        <HelpModal title="About Home" onClose={() => setShowHelp(false)}>
          <HomeHelp />
        </HelpModal>
      )}

      {flashing && !reduceMotion && (
        <div key={flashKey} aria-hidden data-testid="needs-you-flash" className="home-needs-flash pointer-events-none fixed inset-0 z-40" />
      )}

      {!dash ? (
        <p className="text-sm text-fg-muted">Loading…</p>
      ) : isPhone ? (
        <div className="flex flex-col gap-4 min-w-0">
          {needsYou}
          {running}
          {scene}
          {today}
          <RecentTicker items={ticker} latestOnly />
        </div>
      ) : (
        <div className="flex flex-col gap-4 min-h-0 min-w-0">
          {scene}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4 min-w-0">
            {needsYou}
            {running}
            {today}
          </div>
          <RecentTicker items={ticker} />
        </div>
      )}
    </PageFrame>
  )
}
