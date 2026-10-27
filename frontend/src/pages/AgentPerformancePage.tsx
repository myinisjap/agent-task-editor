import { Fragment, useEffect, useState } from 'react'
import { PageFrame } from '../components/ui'
import { useDashboard } from '../lib/useDashboard'
import { formatDuration } from '../lib/format'
import HelpModal from '../components/shared/HelpModal'
import { PerformanceHelp } from '../components/shared/pageHelp'
import { api, type OutcomeQuality, type OutcomeQualityGroupBy, type Repo } from '../api/client'

const GROUP_BY_OPTIONS: { value: OutcomeQualityGroupBy; label: string }[] = [
  { value: 'config', label: 'Agent config' },
  { value: 'version', label: 'Version' },
  { value: 'provider_config', label: 'Provider config' },
  { value: 'repo', label: 'Repo' },
  { value: 'task_type', label: 'Task type' },
]

const selectCls =
  'bg-surface-sunken border border-border rounded px-2.5 py-1 text-xs text-fg focus:outline-none focus:ring-1 focus:ring-accent'

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
    return <span className="text-fg-subtle">—</span>
  }
  const good = goodIsLow ? percent <= 10 : percent >= 80
  const bad = goodIsLow ? percent >= 40 : percent <= 50
  const color = lowSample ? 'text-fg-subtle' : good ? 'text-success' : bad ? 'text-danger' : 'text-warning'
  return (
    <span className={lowSample ? 'opacity-60' : ''}>
      <span className={color}>{percent.toFixed(0)}%</span>
      <span className="text-fg-subtle ml-1">(n={n})</span>
    </span>
  )
}

function HeaderHint({ children, hint, align }: { children: string; hint: string; align: 'left' | 'right' }) {
  return (
    <th className={`${align === 'right' ? 'text-right' : 'text-left'} px-4 py-2`}>
      <span title={hint} className="underline decoration-dotted decoration-fg-subtle cursor-help">{children}</span>
      <span className="sr-only">: {hint}</span>
    </th>
  )
}

export default function AgentPerformancePage() {
  const { dash } = useDashboard()
  const [showHelp, setShowHelp] = useState(false)
  const [repos, setRepos] = useState<Repo[]>([])
  const [filterRepo, setFilterRepo] = useState('')
  const [groupBy, setGroupBy] = useState<OutcomeQualityGroupBy>('config')
  const [outcomeQuality, setOutcomeQuality] = useState<OutcomeQuality | null>(null)

  useEffect(() => {
    api.repos.list().then(setRepos).catch(() => {})
  }, [])

  useEffect(() => {
    api.dashboard.outcomeQuality(filterRepo || undefined, groupBy).then(setOutcomeQuality).catch(() => {})
  }, [filterRepo, groupBy])

  return (
    <PageFrame title="Agent Performance" help={{ onClick: () => setShowHelp(true), title: 'About agent performance' }}>

      {showHelp && (
        <HelpModal title="About Agent Performance" onClose={() => setShowHelp(false)}>
          <PerformanceHelp />
        </HelpModal>
      )}

      {/* Outcome quality: leads with cost-to-done and rework, since those
          change decisions in a way success_rate_percent alone cannot. */}
      <section className="mb-8">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide">
            Outcome quality
          </h2>
          <div className="flex items-center gap-3">
          <label className="flex items-center gap-1.5 text-xs text-fg-muted">
            Break down by
            <select
              aria-label="Break down by"
              value={groupBy}
              onChange={(e) => setGroupBy(e.target.value as OutcomeQualityGroupBy)}
              className={selectCls}
            >
              {GROUP_BY_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
          </label>
          <select value={filterRepo} onChange={(e) => setFilterRepo(e.target.value)} className={selectCls}>
            <option value="">All repos</option>
            {repos.map((r) => (
              <option key={r.id} value={r.id}>{r.name}</option>
            ))}
          </select>
          </div>
        </div>

        {outcomeQuality && outcomeQuality.configs.length > 0 && (
          <div className="bg-surface-raised rounded-lg border border-border overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-fg-subtle border-b border-border">
                  <th className="text-left px-4 py-2">Agent config</th>
                  {outcomeQuality.group_by !== 'config' && (
                    <th className="text-left px-4 py-2">{GROUP_BY_OPTIONS.find((o) => o.value === outcomeQuality.group_by)?.label ?? 'Group'}</th>
                  )}
                  <th className="text-left px-4 py-2">Provider</th>
                  <HeaderHint align="right" hint="A task counts toward the agent config of its last run.">Tasks done</HeaderHint>
                  <HeaderHint align="right" hint="Average total spend across every run of a task until it reached a terminal label. Attributed to the last run’s config.">Cost to done</HeaderHint>
                  <HeaderHint align="right" hint="Share of finished tasks bounced back into a label they already occupied. The numerator is attributed to the run that caused the bounce-back.">Rework rate</HeaderHint>
                  <HeaderHint align="right" hint="Share of finished tasks that needed at least one human-triggered move, not just a final approval.">Human-touch rate</HeaderHint>
                  <HeaderHint align="right" hint="Average review comments received per finished task.">Review burden</HeaderHint>
                  <HeaderHint align="right" hint="Share of finished runs that ended waiting on a human instead of completing.">Escalation rate</HeaderHint>
                </tr>
              </thead>
              <tbody>
                {outcomeQuality.configs.map((c) => (
                  <Fragment key={c.group_key}>
                  <tr
                    className={`border-b border-border last:border-0 ${c.low_sample ? 'opacity-50' : ''}`}
                    title={c.low_sample ? 'Fewer than 5 tasks done: low sample' : undefined}
                  >
                    <td className="px-4 py-2.5 text-fg">{c.agent_name}</td>
                    {outcomeQuality.group_by !== 'config' && (
                      <td className="px-4 py-2.5 text-fg-muted text-xs">{c.group_value || '—'}</td>
                    )}
                    <td className="px-4 py-2.5 text-fg-muted text-xs">{c.provider}</td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs text-right">{c.tasks_done.toLocaleString()}</td>
                    <td className="px-4 py-2.5 text-fg text-xs text-right">
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
                    <td className="px-4 py-2.5 text-fg-muted text-xs text-right">
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
                  {c.ladder_rungs && c.ladder_rungs.length > 0 && (
                    <tr className="border-b border-border last:border-0">
                      <td colSpan={outcomeQuality.group_by !== 'config' ? 9 : 8} className="px-4 pb-2.5 text-xs text-fg-subtle">
                        Finished by rung: {c.ladder_rungs.map((r) => `r${r.rung} ×${r.tasks_done} ($${r.avg_cost_to_done_usd.toFixed(4)} avg)`).join(' · ')}
                      </td>
                    </tr>
                  )}
                  </Fragment>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {outcomeQuality && outcomeQuality.configs.length === 0 && (
          <p className="text-sm text-fg-subtle">No outcome-quality data yet.</p>
        )}

        {!outcomeQuality && <p className="text-sm text-fg-muted">Loading…</p>}

      </section>

      {/* Per-agent-config performance */}
      {dash && dash.agent_config_stats && dash.agent_config_stats.length > 0 && (
        <section className="mb-8">
          <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide mb-3">
            Agent config performance
          </h2>
          <div className="bg-surface-raised rounded-lg border border-border overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-fg-subtle border-b border-border">
                  <th className="text-left px-4 py-2">Agent config</th>
                  <th className="text-left px-4 py-2">Provider</th>
                  <th className="text-right px-4 py-2">Runs</th>
                  <HeaderHint align="right" hint="Completed/failed/waiting-human counts. Measures whether a run exited cleanly, not whether the work stuck.">Success rate</HeaderHint>
                  <th className="text-right px-4 py-2">Avg duration</th>
                  <th className="text-right px-4 py-2">P90 duration</th>
                  <HeaderHint align="right" hint="agent_runs rows per done task (dispatch/retry cycles), not LLM turns capped by Max turns. Credit is split across every config that contributed a run.">Avg runs/task</HeaderHint>
                  <HeaderHint align="right" hint="Snapshot of the task’s current retry counter, attributed to its last run’s config. Resets on success or escalation; not a lifetime count.">Retries</HeaderHint>
                  <th className="text-right px-4 py-2">Cost</th>
                </tr>
              </thead>
              <tbody>
                {dash.agent_config_stats.map((s) => (
                  <tr key={s.agent_config_id} className="border-b border-border last:border-0">
                    <td className="px-4 py-2.5 text-fg">{s.agent_name}</td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs">{s.provider}</td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs text-right">{s.run_count.toLocaleString()}</td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      <span
                        className={
                          s.success_rate_percent >= 80
                            ? 'text-success'
                            : s.success_rate_percent >= 50
                              ? 'text-warning'
                              : 'text-danger'
                        }
                      >
                        {s.success_rate_percent.toFixed(0)}%
                      </span>
                      <span className="text-fg-subtle ml-1">
                        ({s.completed_count}/{s.failed_count}/{s.waiting_human_count})
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs text-right">{formatDuration(s.avg_duration_secs)}</td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs text-right">{formatDuration(s.p90_duration_secs)}</td>
                    <td className="px-4 py-2.5 text-fg-muted text-xs text-right">{s.avg_runs_per_task.toFixed(1)}</td>
                    <td className="px-4 py-2.5 text-xs text-right">
                      {s.tasks_with_retries > 0 ? (
                        <span className="text-warning">
                          {s.tasks_with_retries} task{s.tasks_with_retries === 1 ? '' : 's'} ({s.avg_transient_retries.toFixed(1)} avg)
                        </span>
                      ) : (
                        <span className="text-fg-subtle">0</span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-fg text-xs text-right">
                      {s.cost_usd > 0 ? `$${s.cost_usd.toFixed(4)}` : '$0.00'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {dash && (!dash.agent_config_stats || dash.agent_config_stats.length === 0) && (
        <p className="text-sm text-fg-subtle">No agent config performance data yet.</p>
      )}

      {!dash && (
        <p className="text-sm text-fg-muted">Loading…</p>
      )}
    </PageFrame>
  )
}
