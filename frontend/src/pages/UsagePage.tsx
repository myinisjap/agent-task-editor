import { useNavigate } from 'react-router-dom'
import { useDashboard } from '../lib/useDashboard'

export default function UsagePage() {
  const navigate = useNavigate()
  const { dash } = useDashboard()

  return (
    <div className="p-6 max-w-5xl mx-auto">
      <h1 className="text-xl font-semibold text-slate-100 mb-6">Cost &amp; Usage</h1>

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
            <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-x-auto">
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
            <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-x-auto mt-3">
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
            <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-x-auto mt-3">
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

      {dash &&
        !dash.claude_usage?.available &&
        !(dash.cost_total && (dash.cost_total.input_tokens > 0 || dash.cost_total.output_tokens > 0 || (dash.cost_by_provider?.length ?? 0) > 0)) && (
          <p className="text-sm text-slate-500">No usage data yet.</p>
        )}

      {!dash && (
        <p className="text-sm text-slate-400">Loading…</p>
      )}
    </div>
  )
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
