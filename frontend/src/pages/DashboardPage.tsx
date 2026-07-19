import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { useDashboard } from '../lib/useDashboard'
import { useWorkflowStore } from '../stores/workflow'
import TaskFactory from '../components/TaskFactory'

const VISUALIZE_KEY = 'dashboard.visualize'
const ROBOTS_KEY = 'dashboard.visualize.robots'

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
  const [robots, setRobots] = useState(() => {
    try { return localStorage.getItem(ROBOTS_KEY) === '1' } catch { return false }
  })
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

  const toggleRobots = () => {
    setRobots((v) => {
      const next = !v
      try { localStorage.setItem(ROBOTS_KEY, next ? '1' : '0') } catch { /* ignore */ }
      return next
    })
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
            <button
              onClick={toggleRobots}
              className={`flex items-center gap-1.5 px-2.5 py-1 text-xs rounded-full border transition-colors ${
                robots
                  ? 'bg-slate-800 border-slate-600 text-slate-200'
                  : 'bg-slate-900 border-slate-800 text-slate-500 hover:text-slate-300'
              }`}
              title="Render the crew as robots"
            >
              <span className={`inline-block w-2 h-2 rounded-full ${robots ? 'bg-cyan-400' : 'bg-slate-600'}`} />
              Robots
            </button>
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
        </div>
      </div>

      {/* Label count chips */}
      {dash && Object.keys(dash.label_counts).length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">Task counts by label</h2>
          {visualize && workflow ? (
            <TaskFactory workflow={workflow} labelCounts={dash.label_counts} robots={robots} />
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
          <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden">
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
