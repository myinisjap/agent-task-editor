import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Dashboard } from '../api/client'
import { wsClient } from '../api/ws'

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
  const [dash, setDash] = useState<Dashboard | null>(null)
  const [rejectNote, setRejectNote] = useState<Record<string, string>>({})
  const [pending, setPending] = useState<Record<string, boolean>>({})

  const refresh = useCallback(() => {
    api.dashboard.get().then(setDash).catch(() => {})
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  // Re-fetch on any task-level WS event
  useEffect(() => {
    return wsClient.on((event) => {
      if (
        event.type === 'task.label_changed' ||
        event.type === 'task.agent_started' ||
        event.type === 'task.agent_done' ||
        event.type === 'task.needs_human'
      ) {
        refresh()
      }
    })
  }, [refresh])

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
      <h1 className="text-xl font-semibold text-slate-100 mb-6">Dashboard</h1>

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

      {/* Claude usage (live 5-hour / weekly rate-limit utilization) */}
      {dash && dash.claude_usage?.available && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">Claude usage</h2>
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-4 flex flex-col gap-4">
            <UsageBar
              label="5-hour window"
              percent={dash.claude_usage.five_hour_percent ?? 0}
              resetsAt={dash.claude_usage.five_hour_resets_at}
            />
            <UsageBar
              label="Weekly window"
              percent={dash.claude_usage.weekly_percent ?? 0}
              resetsAt={dash.claude_usage.weekly_resets_at}
            />
          </div>
        </section>
      )}

      {/* Cost & usage */}
      {dash && dash.cost_total && (dash.cost_total.input_tokens > 0 || dash.cost_total.output_tokens > 0 || (dash.cost_by_provider?.length ?? 0) > 0) && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">Cost &amp; usage</h2>
          <div className="bg-slate-900 rounded-lg border border-slate-800 p-4 mb-3">
            <div className="flex flex-wrap gap-6">
              <div>
                <div className="text-xs text-slate-500">Total cost</div>
                <div className="text-lg font-semibold text-slate-100">
                  {dash.cost_total.cost_usd > 0 ? `$${dash.cost_total.cost_usd.toFixed(4)}` : '$0.00'}
                </div>
              </div>
              <div>
                <div className="text-xs text-slate-500">Input tokens</div>
                <div className="text-lg font-semibold text-slate-100">
                  {dash.cost_total.input_tokens.toLocaleString()}
                </div>
              </div>
              <div>
                <div className="text-xs text-slate-500">Output tokens</div>
                <div className="text-lg font-semibold text-slate-100">
                  {dash.cost_total.output_tokens.toLocaleString()}
                </div>
              </div>
            </div>
            <p className="text-xs text-slate-500 mt-3">
              Estimated cost, computed from a token-based pricing table for anthropic/llm providers; the claude
              CLI reports its own authoritative cost (which may be $0 under a Claude Max subscription).
            </p>
          </div>

          {dash.cost_by_provider && dash.cost_by_provider.length > 0 && (
            <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-xs text-slate-500 border-b border-slate-800">
                    <th className="text-left px-4 py-2">Provider</th>
                    <th className="text-right px-4 py-2">Runs</th>
                    <th className="text-right px-4 py-2">Input tok</th>
                    <th className="text-right px-4 py-2">Output tok</th>
                    <th className="text-right px-4 py-2">Cost</th>
                  </tr>
                </thead>
                <tbody>
                  {dash.cost_by_provider.map((p) => (
                    <tr key={p.provider} className="border-b border-slate-800 last:border-0">
                      <td className="px-4 py-2.5 text-slate-200">{p.provider}</td>
                      <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{p.run_count.toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{p.input_tokens.toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{p.output_tokens.toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-slate-200 text-xs text-right">
                        {p.cost_usd > 0 ? `$${p.cost_usd.toFixed(4)}` : '$0.00'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {dash.cost_by_day && dash.cost_by_day.length > 0 && (
            <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden mt-3">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-xs text-slate-500 border-b border-slate-800">
                    <th className="text-left px-4 py-2">Day</th>
                    <th className="text-right px-4 py-2">Runs</th>
                    <th className="text-right px-4 py-2">Input tok</th>
                    <th className="text-right px-4 py-2">Output tok</th>
                    <th className="text-right px-4 py-2">Cost</th>
                    <th className="text-left px-4 py-2 w-32"></th>
                  </tr>
                </thead>
                <tbody>
                  {(() => {
                    const maxDayCost = Math.max(...dash.cost_by_day.map((d) => d.cost_usd), 0.0001)
                    return dash.cost_by_day.map((d) => (
                      <tr key={d.day} className="border-b border-slate-800 last:border-0">
                        <td className="px-4 py-2.5 text-slate-200 text-xs">{d.day}</td>
                        <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{d.run_count.toLocaleString()}</td>
                        <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{d.input_tokens.toLocaleString()}</td>
                        <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{d.output_tokens.toLocaleString()}</td>
                        <td className="px-4 py-2.5 text-slate-200 text-xs text-right">
                          {d.cost_usd > 0 ? `$${d.cost_usd.toFixed(4)}` : '$0.00'}
                        </td>
                        <td className="px-4 py-2.5">
                          <div className="w-28 h-1.5 rounded-full bg-slate-800 overflow-hidden">
                            <div
                              className="h-full rounded-full bg-indigo-500"
                              style={{ width: `${(d.cost_usd / maxDayCost) * 100}%` }}
                            />
                          </div>
                        </td>
                      </tr>
                    ))
                  })()}
                </tbody>
              </table>
            </div>
          )}

          {dash.cost_by_task && dash.cost_by_task.length > 0 && (
            <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-hidden mt-3">
              <div className="px-4 pt-3 pb-1 text-xs text-slate-500">Top tasks by cost</div>
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-xs text-slate-500 border-b border-slate-800">
                    <th className="text-left px-4 py-2">Task</th>
                    <th className="text-right px-4 py-2">Input tok</th>
                    <th className="text-right px-4 py-2">Output tok</th>
                    <th className="text-right px-4 py-2">Cost</th>
                  </tr>
                </thead>
                <tbody>
                  {dash.cost_by_task.map((tc) => (
                    <tr key={tc.task_id} className="border-b border-slate-800 last:border-0">
                      <td className="px-4 py-2.5 text-slate-200">
                        <button
                          onClick={() => navigate(`/tasks/${tc.task_id}`)}
                          className="hover:text-white hover:underline truncate max-w-xs text-left"
                        >
                          {tc.task_title || tc.task_id}
                        </button>
                      </td>
                      <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{tc.input_tokens.toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{tc.output_tokens.toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-slate-200 text-xs text-right">
                        {tc.cost_usd > 0 ? `$${tc.cost_usd.toFixed(4)}` : '$0.00'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}

      {/* Per-agent-config performance */}
      {dash && dash.agent_config_stats && dash.agent_config_stats.length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide mb-3">
            Agent config performance
          </h2>
          <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-slate-500 border-b border-slate-800">
                  <th className="text-left px-4 py-2">Agent config</th>
                  <th className="text-left px-4 py-2">Provider</th>
                  <th className="text-right px-4 py-2">Runs</th>
                  <th className="text-right px-4 py-2">Success rate</th>
                  <th className="text-right px-4 py-2">Avg duration</th>
                  <th className="text-right px-4 py-2">P90 duration</th>
                  <th className="text-right px-4 py-2">Avg turns/task</th>
                  <th className="text-right px-4 py-2">Retries</th>
                  <th className="text-right px-4 py-2">Cost</th>
                </tr>
              </thead>
              <tbody>
                {dash.agent_config_stats.map((s) => (
                  <tr key={s.agent_config_id} className="border-b border-slate-800 last:border-0">
                    <td className="px-4 py-2.5 text-slate-200">{s.agent_name}</td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs">{s.provider}</td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{s.run_count.toLocaleString()}</td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      <span
                        className={
                          s.success_rate_percent >= 80
                            ? 'text-emerald-400'
                            : s.success_rate_percent >= 50
                              ? 'text-amber-400'
                              : 'text-red-400'
                        }
                      >
                        {s.success_rate_percent.toFixed(0)}%
                      </span>
                      <span className="text-slate-500 ml-1">
                        ({s.completed_count}/{s.failed_count}/{s.waiting_human_count})
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{formatDuration(s.avg_duration_secs)}</td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{formatDuration(s.p90_duration_secs)}</td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{s.avg_turns_to_done.toFixed(1)}</td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      {s.tasks_with_retries > 0 ? (
                        <span className="text-amber-400">
                          {s.tasks_with_retries} task{s.tasks_with_retries === 1 ? '' : 's'} ({s.avg_transient_retries.toFixed(1)} avg)
                        </span>
                      ) : (
                        <span className="text-slate-500">0</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-slate-200 text-xs text-right">
                      {s.cost_usd > 0 ? `$${s.cost_usd.toFixed(4)}` : '$0.00'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="text-xs text-slate-500 mt-3">
            Success rate shows completed/failed/waiting-human counts. "Avg turns/task" and the retry
            snapshot are attributed to a task's <em>last</em> run's agent config, and the retry count
            reflects the task's current retry counter, which resets to 0 on success or escalation to a
            human — it's not a lifetime count of every retry that ever happened.
          </p>
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

/** Formats a duration in seconds as "Xm Ys" (or "Xs" under a minute). */
function formatDuration(secs: number): string {
  if (!secs || secs <= 0) return '—'
  const mins = Math.floor(secs / 60)
  const rem = Math.round(secs % 60)
  return mins > 0 ? `${mins}m ${rem}s` : `${rem}s`
}

/** A single 5h/weekly Claude usage row: label, percentage, progress bar, reset time. */
function UsageBar({ label, percent, resetsAt }: { label: string; percent: number; resetsAt?: string | null }) {
  const clamped = Math.min(100, Math.max(0, percent))
  const barColor = clamped >= 95 ? 'bg-red-500' : clamped >= 80 ? 'bg-amber-500' : 'bg-emerald-500'
  const textColor = clamped >= 95 ? 'text-red-400' : clamped >= 80 ? 'text-amber-400' : 'text-slate-300'

  return (
    <div>
      <div className="flex items-center justify-between mb-1.5">
        <span className="text-sm text-slate-300">{label}</span>
        <span className={`text-sm font-semibold ${textColor}`}>{clamped.toFixed(0)}%</span>
      </div>
      <div className="w-full h-2 rounded-full bg-slate-800 overflow-hidden">
        <div
          className={`h-full rounded-full ${barColor}`}
          style={{ width: `${clamped}%` }}
        />
      </div>
      {resetsAt && (
        <div className="text-xs text-slate-500 mt-1">
          Resets {new Date(resetsAt).toLocaleString()}
        </div>
      )}
    </div>
  )
}
