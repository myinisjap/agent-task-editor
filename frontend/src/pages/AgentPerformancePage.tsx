import { useEffect, useState } from 'react'
import { useDashboard } from '../lib/useDashboard'
import { formatDuration } from '../lib/format'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
import { PerformanceHelp } from '../components/shared/pageHelp'
import { api, type OutcomeQuality, type Repo } from '../api/client'

const selectCls =
  'bg-slate-800 border border-slate-700 rounded px-2.5 py-1 text-xs text-slate-200 focus:outline-none focus:ring-1 focus:ring-indigo-500'

// Rate cells are greyed out (rather than hidden) below this sample size so a
// confident-looking percentage doesn't mislead when it's backed by only a
// couple of tasks — see OutcomeQuality's low_sample_* flags.
function RateCell({
  percent,
  n,
  lowSample,
  goodIsLow,
}: {
  percent: number
  n: number
  lowSample: boolean
  goodIsLow: boolean
}) {
  if (n === 0) {
    return <span className="text-slate-600">—</span>
  }
  const good = goodIsLow ? percent <= 10 : percent >= 80
  const bad = goodIsLow ? percent >= 40 : percent <= 50
  const color = lowSample ? 'text-slate-500' : good ? 'text-emerald-400' : bad ? 'text-red-400' : 'text-amber-400'
  return (
    <span className={lowSample ? 'opacity-60' : ''}>
      <span className={color}>{percent.toFixed(0)}%</span>
      <span className="text-slate-500 ml-1">(n={n})</span>
    </span>
  )
}

export default function AgentPerformancePage() {
  const { dash } = useDashboard()
  const [showHelp, setShowHelp] = useState(false)
  const [repos, setRepos] = useState<Repo[]>([])
  const [filterRepo, setFilterRepo] = useState('')
  const [outcomeQuality, setOutcomeQuality] = useState<OutcomeQuality | null>(null)

  useEffect(() => {
    api.repos.list().then(setRepos).catch(() => {})
  }, [])

  useEffect(() => {
    api.dashboard.outcomeQuality(filterRepo || undefined).then(setOutcomeQuality).catch(() => {})
  }, [filterRepo])

  return (
    <div className="p-6 max-w-6xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-slate-100">Agent Performance</h1>
        <HelpButton onClick={() => setShowHelp(true)} title="About agent performance" />
      </div>

      {showHelp && (
        <HelpModal title="About Agent Performance" onClose={() => setShowHelp(false)}>
          <PerformanceHelp />
        </HelpModal>
      )}

      {/* Outcome quality: leads with cost-to-done and rework, since those
          change decisions in a way success_rate_percent alone cannot. */}
      <section className="mb-8">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-xs font-medium text-slate-500 uppercase tracking-wide">
            Outcome quality
          </h2>
          <select value={filterRepo} onChange={(e) => setFilterRepo(e.target.value)} className={selectCls}>
            <option value="">All repos</option>
            {repos.map((r) => (
              <option key={r.id} value={r.id}>{r.name}</option>
            ))}
          </select>
        </div>

        {outcomeQuality && outcomeQuality.configs.length > 0 && (
          <div className="bg-slate-900 rounded-lg border border-slate-800 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-slate-500 border-b border-slate-800">
                  <th className="text-left px-4 py-2">Agent config</th>
                  <th className="text-left px-4 py-2">Provider</th>
                  <th className="text-right px-4 py-2">Tasks done</th>
                  <th className="text-right px-4 py-2">Cost to done</th>
                  <th className="text-right px-4 py-2">Rework rate</th>
                  <th className="text-right px-4 py-2">Human-touch rate</th>
                  <th className="text-right px-4 py-2">Review burden</th>
                  <th className="text-right px-4 py-2">Escalation rate</th>
                </tr>
              </thead>
              <tbody>
                {outcomeQuality.configs.map((c) => (
                  <tr key={c.agent_config_id} className="border-b border-slate-800 last:border-0">
                    <td className="px-4 py-2.5 text-slate-200">{c.agent_name}</td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs">{c.provider}</td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs text-right">{c.tasks_done.toLocaleString()}</td>
                    <td className="px-4 py-2.5 text-slate-200 text-xs text-right">
                      {c.tasks_done > 0 ? `$${c.avg_cost_to_done_usd.toFixed(4)}` : '—'}
                    </td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      <RateCell percent={c.rework_rate_percent} n={c.rework_n} lowSample={c.low_sample_rework} goodIsLow />
                    </td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      <RateCell
                        percent={c.human_touch_rate_percent}
                        n={c.human_touch_n}
                        lowSample={c.low_sample_human_touch}
                        goodIsLow
                      />
                    </td>
                    <td className="px-4 py-2.5 text-slate-400 text-xs text-right">
                      {c.tasks_done > 0 ? c.avg_review_comments.toFixed(1) : '—'}
                    </td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      <RateCell
                        percent={c.escalation_rate_percent}
                        n={c.runs_finished}
                        lowSample={c.low_sample_escalation}
                        goodIsLow
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {outcomeQuality && outcomeQuality.configs.length === 0 && (
          <p className="text-sm text-slate-500">No outcome-quality data yet.</p>
        )}

        {!outcomeQuality && <p className="text-sm text-slate-400">Loading…</p>}

        <p className="text-xs text-slate-500 mt-3">
          Rates below n=10 are greyed out as low-sample. "Tasks done" and every rate derived from
          it attribute a task to its <em>last</em> run's agent config, except rework's numerator,
          which is attributed to whichever run caused the bounce-back. See the help panel above
          for full definitions.
        </p>
      </section>

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
            Success rate shows completed/failed/waiting-human counts — it measures whether a run
            exited cleanly, not whether the work stuck; see "Outcome quality" above for that.
            "Avg turns/task" and the retry snapshot are attributed to a task's <em>last</em> run's
            agent config, and the retry count reflects the task's current retry counter, which
            resets to 0 on success or escalation to a human — it's not a lifetime count of every
            retry that ever happened.
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
