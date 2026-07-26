import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { useDashboard } from '../lib/useDashboard'
import { useWorkflowStore } from '../stores/workflow'
import TaskFactory from '../components/TaskFactory'
import FactoryLine from '../components/FactoryLine'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
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

const MODES: { value: VizMode; label: string }[] = [
  { value: 'office', label: 'Office' },
  { value: 'robots', label: 'Robots' },
  { value: 'factory', label: 'Factory' },
]

const LABEL_COLORS: Record<string, string> = {
  not_ready:    '#6B7280',
  plan:         '#8B5CF6',
  'review-plan': '#3B82F6',
  work:         '#F59E0B',
  testing:      '#F97316',
  'agent-review': '#6366F1',
  review:       '#EC4899',
  done:         '#10B981',
}

export default function DashboardPage() {
  const navigate = useNavigate()
  const { dash, refresh } = useDashboard()
  const [rejectNote, setRejectNote] = useState<Record<string, string>>({})
  const [pending, setPending] = useState<Record<string, boolean>>({})
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

  const handleApprove = async (taskId: string) => {
    setPending((p) => ({ ...p, [taskId]: true }))
    try {
      await api.tasks.approve(taskId)
      refresh()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setPending((p) => ({ ...p, [taskId]: false }))
    }
  }

  const handleReject = async (taskId: string) => {
    const note = rejectNote[taskId] ?? ''
    if (!note.trim()) return
    setPending((p) => ({ ...p, [taskId]: true }))
    try {
      await api.tasks.reject(taskId, note)
      setRejectNote((n) => ({ ...n, [taskId]: '' }))
      refresh()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setPending((p) => ({ ...p, [taskId]: false }))
    }
  }

  return (
    <div className="p-6 max-w-5xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-slate-100">Overview</h1>
        <div className="flex items-center gap-2">
          {visualize && (
            <div className="inline-flex p-0.5 rounded-full border border-slate-800 bg-slate-900" role="group" aria-label="Visualization style">
              {MODES.map((m) => (
                <button
                  key={m.value}
                  onClick={() => selectMode(m.value)}
                  aria-pressed={mode === m.value}
                  className={`px-2.5 py-1 text-xs rounded-full transition-colors ${
                    mode === m.value
                      ? 'bg-slate-800 text-slate-100 shadow-sm'
                      : 'text-slate-500 hover:text-slate-300'
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
                ? 'bg-slate-800 border-slate-600 text-slate-200'
                : 'bg-slate-900 border-slate-800 text-slate-500 hover:text-slate-300'
            }`}
            title="Fun, non-essential task visualization"
          >
            <span className={`inline-block w-2 h-2 rounded-full ${visualize ? 'bg-emerald-400' : 'bg-slate-600'}`} />
            Visualize tasks
          </button>
          <HelpButton onClick={() => setShowHelp(true)} title="About this page" />
        </div>
      </div>

      {showHelp && (
        <HelpModal title="About the Overview" onClose={() => setShowHelp(false)}>
          <DashboardHelp />
        </HelpModal>
      )}

      {/* Label count chips */}
      {dash && Object.keys(dash.label_counts).length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">Task counts by label</h2>
          {visualize && workflow ? (
            mode === 'factory' ? (
              <FactoryLine workflow={workflow} labelCounts={dash.label_counts} />
            ) : (
              <TaskFactory workflow={workflow} labelCounts={dash.label_counts} robots={mode === 'robots'} />
            )
          ) : (
            <div className="flex flex-wrap gap-2">
              {Object.entries(dash.label_counts).map(([label, count]) => (
                <div
                  key={label}
                  className="flex items-center gap-2 px-3 py-1.5 rounded-full text-white text-xs font-medium"
                  style={{ backgroundColor: LABEL_COLORS[label] ?? '#6B7280' }}
                >
                  <span>{label}</span>
                  <span className="bg-black/20 rounded-full px-1.5 py-0.5 text-xs">{count}</span>
                </div>
              ))}
            </div>
          )}
        </section>
      )}

      {/* Active agents */}
      {dash && dash.active_agents.length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
            Active agents ({dash.active_agents.length})
          </h2>
          <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-slate-500 border-b border-slate-800">
                  <th className="text-left px-4 py-2">Task</th>
                  <th className="text-left px-4 py-2">Agent</th>
                  <th className="text-left px-4 py-2">Started</th>
                  <th className="px-4 py-2" />
                </tr>
              </thead>
              <tbody>
                {dash.active_agents.map((a) => (
                  <tr key={a.run_id} className="border-b border-slate-800 last:border-0">
                    <td className="px-4 py-2.5 text-slate-200">
                      <button
                        onClick={() => navigate(`/tasks/${a.task_id}`)}
                        className="hover:text-white hover:underline truncate max-w-xs text-left"
                      >
                        {a.task_title}
                      </button>
                    </td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs">{a.agent_name}</td>
                    <td className="px-4 py-2.5 text-slate-500 text-xs">
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
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
            Repo concurrency
          </h2>
          <div className="flex flex-col gap-2">
            {dash.repo_concurrency.map((rc) => {
              const pct = rc.limit > 0 ? Math.min(100, (rc.in_use / rc.limit) * 100) : 0
              const saturated = rc.in_use >= rc.limit
              return (
                <div key={rc.repo_id} className="bg-slate-900 border border-slate-800 rounded-lg px-4 py-2.5">
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-sm text-slate-200 truncate">{rc.repo_name}</span>
                    <span className={`text-xs shrink-0 ${saturated ? 'text-amber-400' : 'text-slate-400'}`}>
                      {rc.in_use} / {rc.limit} workers
                    </span>
                  </div>
                  <div className="h-1.5 rounded-full bg-slate-800 overflow-hidden">
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

      {/* Intervention queue */}
      {dash && dash.intervention_queue.length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
            Needs your input ({dash.intervention_queue.length})
          </h2>
          <div className="flex flex-col gap-3">
            {dash.intervention_queue.map((item) => (
              <div key={item.run_id} className="bg-slate-900 border border-pink-900/50 rounded-lg p-4">
                <div className="flex items-start justify-between gap-4 mb-3">
                  <div>
                    <button
                      onClick={() => navigate(`/tasks/${item.task_id}`)}
                      className="text-sm font-medium text-slate-200 hover:text-white hover:underline text-left"
                    >
                      {item.task_title}
                    </button>
                    {item.message && (
                      <p className="text-xs text-slate-400 mt-1">{item.message}</p>
                    )}
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">
                    {new Date(item.created_at).toLocaleTimeString()}
                  </span>
                </div>
                <div className="flex gap-2 items-start">
                  <input
                    type="text"
                    value={rejectNote[item.task_id] ?? ''}
                    onChange={(e) =>
                      setRejectNote((n) => ({ ...n, [item.task_id]: e.target.value }))
                    }
                    placeholder="Rejection note…"
                    className="flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-2.5 py-1.5 text-slate-200 placeholder-slate-500 focus:outline-none focus:border-slate-500"
                  />
                  <button
                    onClick={() => handleApprove(item.task_id)}
                    disabled={pending[item.task_id]}
                    className="px-3 py-1.5 text-xs font-medium rounded bg-emerald-600 hover:bg-emerald-500 text-white disabled:opacity-50"
                  >
                    Approve
                  </button>
                  <button
                    onClick={() => handleReject(item.task_id)}
                    disabled={pending[item.task_id] || !(rejectNote[item.task_id] ?? '').trim()}
                    className="px-3 py-1.5 text-xs font-medium rounded bg-red-700 hover:bg-red-600 text-white disabled:opacity-50"
                  >
                    Reject
                  </button>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {dash && !dash.active_agents.length && !dash.intervention_queue.length && (
        <p className="text-sm text-slate-500">No active agents or pending reviews.</p>
      )}

      {!dash && (
        <p className="text-sm text-slate-400">Loading…</p>
      )}
    </div>
  )
}
