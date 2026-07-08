import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { useDashboard } from '../lib/useDashboard'

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
      <h1 className="text-xl font-semibold text-slate-100 mb-6">Overview</h1>

      {/* Label count chips */}
      {dash && Object.keys(dash.label_counts).length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">Task counts by label</h2>
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
