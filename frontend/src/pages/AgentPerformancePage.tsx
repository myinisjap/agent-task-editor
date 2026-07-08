import { useEffect, useState, useCallback } from 'react'
import { api, type Dashboard } from '../api/client'
import { wsClient } from '../api/ws'
import { formatDuration } from '../lib/format'

export default function AgentPerformancePage() {
  const [dash, setDash] = useState<Dashboard | null>(null)

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

  return (
    <div className="p-6 max-w-6xl mx-auto">
      <h1 className="text-xl font-semibold text-slate-100 mb-6">Agent Performance</h1>

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

      {dash && (!dash.agent_config_stats || dash.agent_config_stats.length === 0) && (
        <p className="text-sm text-slate-500">No agent config performance data yet.</p>
      )}

      {!dash && (
        <p className="text-sm text-slate-400">Loading…</p>
      )}
    </div>
  )
}
