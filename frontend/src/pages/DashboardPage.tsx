import { useEffect, useState } from 'react'
import { PageFrame } from '../components/ui'
import { Link, useNavigate } from 'react-router-dom'
import { type Workflow } from '../api/client'
import { buildChipRows } from '../lib/dashboardChips'
import { useDashboard } from '../lib/useDashboard'
import { useNeedsMeStore } from '../stores/needsMe'
import { useInboxStore, useInboxSections } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { inboxCount, SECTION_ORDER } from '../lib/inbox'
import { useWorkflowStore } from '../stores/workflow'
import TaskFactory from '../components/TaskFactory'
import FactoryLine from '../components/FactoryLine'
import HelpModal from '../components/shared/HelpModal'
import { DashboardHelp } from '../components/shared/pageHelp'

const VISUALIZE_KEY = 'dashboard.visualize'
const ROBOTS_KEY = 'dashboard.visualize.robots' // legacy; migrated into MODE_KEY
const MODE_KEY = 'dashboard.visualize.mode'

type VizMode = 'office' | 'robots' | 'factory'

// Read the persisted visualization mode, migrating the old boolean "Robots"
// toggle: an existing robots='1' becomes the 'robots' mode on first read.
function initialMode(): VizMode {
  try {
    const m = localStorage.getItem(MODE_KEY)
    if (m === 'office' || m === 'robots' || m === 'factory') return m
    if (localStorage.getItem(ROBOTS_KEY) === '1') return 'robots'
  } catch { /* ignore */ }
  return 'office'
}

const SECTION_TITLE = { decide: 'Decide', answer: 'Answer', fix: 'Fix setup' } as const

const MODES: { value: VizMode; label: string }[] = [
  { value: 'office', label: 'Office' },
  { value: 'robots', label: 'Robots' },
  { value: 'factory', label: 'Factory' },
]

function LabelChipRows({
  workflows,
  labelCounts,
  workflowLabelCounts,
}: {
  workflows: Workflow[]
  labelCounts: Record<string, number>
  workflowLabelCounts?: Record<string, Record<string, number>>
}) {
  const rows = buildChipRows(workflows, labelCounts, workflowLabelCounts)
  return (
    <div className="flex flex-col gap-3">
      {rows.map((row) => (
        <div key={row.key}>
          {row.title && <p className="text-xs text-fg-subtle mb-1.5">{row.title}</p>}
          <div className="flex flex-wrap gap-2">
            {row.chips.map((c) => (
              <div
                key={c.key}
                className="flex items-center gap-2 px-3 py-1.5 rounded-full text-white text-xs font-medium"
                style={{ backgroundColor: c.color }}
              >
                <span>{c.name}</span>
                <span className="bg-black/20 rounded-full px-1.5 py-0.5 text-xs">{c.count}</span>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

export default function DashboardPage() {
  const navigate = useNavigate()
  const { dash } = useDashboard()
  const sections = useInboxSections()
  const count = inboxCount(sections)
  const refreshNeedsMe = useNeedsMeStore((st) => st.refresh)
  const refreshEffects = useInboxStore((st) => st.refresh)
  const fetchHealth = useHealthStore((st) => st.fetch)
  // Make sure every input to the shared count is fresh (the nav syncs also keep them live).
  useEffect(() => {
    void refreshNeedsMe()
    void refreshEffects()
    void fetchHealth()
  }, [refreshNeedsMe, refreshEffects, fetchHealth])
  const [visualize, setVisualize] = useState(() => {
    try { return localStorage.getItem(VISUALIZE_KEY) === '1' } catch { return false }
  })
  const [mode, setMode] = useState<VizMode>(initialMode)
  const [showHelp, setShowHelp] = useState(false)
  const workflows = useWorkflowStore((s) => s.workflows)
  const workflow = useWorkflowStore((s) => s.active())

  useEffect(() => {
    if (workflows.length === 0) useWorkflowStore.getState().fetch()
  }, [workflows.length])

  const toggleVisualize = () => {
    setVisualize((v) => {
      const next = !v
      try { localStorage.setItem(VISUALIZE_KEY, next ? '1' : '0') } catch { /* ignore */ }
      return next
    })
  }

  const selectMode = (next: VizMode) => {
    setMode(next)
    try { localStorage.setItem(MODE_KEY, next) } catch { /* ignore */ }
  }

  return (
    <PageFrame
      title="Overview"
      help={{ onClick: () => setShowHelp(true), title: 'About this page' }}
      actions={
        <div className="flex items-center gap-2">
          {visualize && (
            <div className="inline-flex p-0.5 rounded-full border border-border bg-surface-raised" role="group" aria-label="Visualization style">
              {MODES.map((m) => (
                <button
                  key={m.value}
                  onClick={() => selectMode(m.value)}
                  aria-pressed={mode === m.value}
                  className={`px-2.5 py-1 text-xs rounded-full transition-colors ${
                    mode === m.value
                      ? 'bg-surface-sunken text-fg shadow-sm'
                      : 'text-fg-subtle hover:text-fg-muted'
                  }`}
                  title={
                    m.value === 'factory'
                      ? 'Tasks as items on a factory assembly line'
                      : m.value === 'robots'
                        ? 'Office floor, crew rendered as robots'
                        : 'Office floor with a human crew'
                  }
                >
                  {m.label}
                </button>
              ))}
            </div>
          )}
          <button
            onClick={toggleVisualize}
            className={`flex items-center gap-1.5 px-2.5 py-1 text-xs rounded-full border transition-colors ${
              visualize
                ? 'bg-surface-sunken border-border text-fg'
                : 'bg-surface-raised border-border text-fg-subtle hover:text-fg-muted'
            }`}
            title="Fun, non-essential task visualization"
          >
            <span className={`inline-block w-2 h-2 rounded-full ${visualize ? 'bg-success' : 'bg-fg-subtle'}`} />
            Visualize tasks
          </button>
        </div>
      }
    >

      {showHelp && (
        <HelpModal title="About the Overview" onClose={() => setShowHelp(false)}>
          <DashboardHelp />
        </HelpModal>
      )}

      {count > 0 && (
        <Link
          to="/inbox"
          data-testid="overview-needs-you"
          className="flex items-center justify-between gap-4 mb-8 bg-surface-raised border border-border rounded-lg px-4 py-3 hover:border-accent"
        >
          <div className="min-w-0">
            <p className="text-sm font-medium text-fg">Needs you ({count})</p>
            <p className="text-xs text-fg-muted mt-0.5">
              {SECTION_ORDER.filter((k) => sections[k].length > 0)
                .map((k) => `${SECTION_TITLE[k]} ${sections[k].length}`)
                .join(' · ')}
            </p>
          </div>
          <span className="shrink-0 text-xs text-accent-fg">Open Inbox →</span>
        </Link>
      )}

      {/* Label count chips */}
      {dash && Object.keys(dash.label_counts).length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide mb-3">Task counts by label</h2>
          {visualize && workflow ? (
            mode === 'factory' ? (
              <FactoryLine workflow={workflow} labelCounts={dash.label_counts} />
            ) : (
              <TaskFactory workflow={workflow} labelCounts={dash.label_counts} robots={mode === 'robots'} />
            )
          ) : (
            <LabelChipRows
              workflows={workflows}
              labelCounts={dash.label_counts}
              workflowLabelCounts={dash.workflow_label_counts}
            />
          )}
        </section>
      )}

      {/* Active agents */}
      {dash && dash.active_agents.length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide mb-3">
            Active agents ({dash.active_agents.length})
          </h2>
          <div className="bg-surface-raised rounded-lg border border-border overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-fg-subtle border-b border-border">
                  <th className="text-left px-4 py-2">Task</th>
                  <th className="text-left px-4 py-2">Agent</th>
                  <th className="text-left px-4 py-2">Started</th>
                  <th className="px-4 py-2" />
                </tr>
              </thead>
              <tbody>
                {dash.active_agents.map((a) => (
                  <tr key={a.run_id} className="border-b border-border last:border-0">
                    <td className="px-4 py-2.5 text-fg">
                      <button
                        onClick={() => navigate(`/tasks/${a.task_id}`)}
                        className="hover:text-slate-50 hover:underline truncate max-w-xs text-left"
                      >
                        {a.task_title}
                      </button>
                    </td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs">{a.agent_name}</td>
                    <td className="px-4 py-2.5 text-fg-subtle text-xs">
                      {new Date(a.started_at).toLocaleTimeString()}
                    </td>
                    <td className="px-4 py-2.5">
                      <span className="flex items-center gap-1.5 text-xs text-yellow-400">
                        <span className="inline-block w-1.5 h-1.5 rounded-full bg-yellow-400 animate-pulse" />
                        running
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {/* Per-repo concurrency: worker slots in use vs. each repo's effective
          limit (its max_concurrent_runs if set, else the global MAX_WORKERS).
          Only shown when at least one repo has an in-flight run. */}
      {dash && dash.repo_concurrency.length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide mb-3">
            Repo concurrency
          </h2>
          <div className="flex flex-col gap-2">
            {dash.repo_concurrency.map((rc) => {
              const pct = rc.limit > 0 ? Math.min(100, (rc.in_use / rc.limit) * 100) : 0
              const saturated = rc.in_use >= rc.limit
              return (
                <div key={rc.repo_id} className="bg-surface-raised border border-border rounded-lg px-4 py-2.5">
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-sm text-fg truncate">{rc.repo_name}</span>
                    <span className={`text-xs shrink-0 ${saturated ? 'text-warning' : 'text-fg-muted'}`}>
                      {rc.in_use} / {rc.limit} workers
                    </span>
                  </div>
                  <div className="h-1.5 rounded-full bg-surface-sunken overflow-hidden">
                    <div
                      className={`h-full rounded-full ${saturated ? 'bg-amber-500' : 'bg-indigo-500'}`}
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                </div>
              )
            })}
          </div>
        </section>
      )}

      {dash && !dash.active_agents.length && count === 0 && (
        <p className="text-sm text-fg-subtle">No active agents or pending reviews.</p>
      )}

      {!dash && (
        <p className="text-sm text-fg-muted">Loading…</p>
      )}
    </PageFrame>
  )
}
