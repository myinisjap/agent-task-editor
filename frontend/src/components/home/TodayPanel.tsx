import type { Dashboard } from '../../api/client'
import { Card } from '../ui'
import { CostBudgetBar, UsageBar } from '../insights/UsageBars'

/** UTC calendar day, matching the server's date(completed_at) bucketing. */
export function utcToday(now: Date = new Date()): string {
  return now.toISOString().slice(0, 10)
}

/** "75%" for passed/total, or an en dash when nothing was verified. */
export function verifyPassRate(today?: { verify_total: number; verify_passed: number } | null): string {
  if (!today || today.verify_total <= 0) return '–'
  return Math.round((today.verify_passed / today.verify_total) * 100) + '%'
}

/** Done-today count and verify pass rate (dash.today; absent on older servers). */
function TodayOutcomes({ today }: { today?: Dashboard['today'] }) {
  return (
    <div data-testid="today-outcomes" className="flex gap-6 min-w-0">
      <div>
        <div data-testid="today-done" className="text-4xl xl:text-5xl leading-none tabular-nums font-semibold font-mono text-fg">{today?.done_count ?? 0}</div>
        <div className="text-xs text-fg-muted mt-1">done today</div>
      </div>
      <div>
        <div data-testid="today-verify" className="text-4xl xl:text-5xl leading-none tabular-nums font-semibold font-mono text-fg">{verifyPassRate(today)}</div>
        <div className="text-xs text-fg-muted mt-1">verify pass{today && today.verify_total > 0 ? ` · ${today.verify_passed}/${today.verify_total}` : ''}</div>
      </div>
    </div>
  )
}

export default function TodayPanel({ dash }: { dash: Dashboard }) {
  const budget = dash.global_cost_budget
  const hasDaily = !!budget && budget.daily_limit_usd > 0
  const hasMonthly = !!budget && budget.monthly_limit_usd > 0
  const today = (dash.cost_by_day ?? []).find((d) => d.day === utcToday())
  const usage = dash.claude_usage
  return (
    <Card as="section" padding="md" aria-label="Today" data-testid="today-panel" className="flex flex-col gap-3">
      <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide">Today</h2>
      {budget && (hasDaily || hasMonthly) ? (
        <div className="flex flex-col gap-3">
          {hasDaily && (
            <CostBudgetBar label="Spend today" spent={budget.daily_spent_usd} limit={budget.daily_limit_usd} forecast={budget.daily_forecast_usd} showForecastMarker />
          )}
          {hasMonthly && (
            <CostBudgetBar label="Spend this month" spent={budget.monthly_spent_usd} limit={budget.monthly_limit_usd} forecast={budget.monthly_forecast_usd} showForecastMarker />
          )}
        </div>
      ) : (
        <p data-testid="today-spend" className="text-sm text-fg">
          <span className="text-2xl font-semibold font-mono">${(today?.cost_usd ?? 0).toFixed(2)}</span>{' '}
          <span className="text-fg-muted">today{today ? ` · ${today.run_count} run${today.run_count === 1 ? '' : 's'}` : ''}</span>
        </p>
      )}
      <TodayOutcomes today={dash.today} />
      {usage?.available && (
        <div className="flex flex-col gap-3">
          <UsageBar label="Claude 5-hour" percent={usage.five_hour_percent} resetsAt={usage.five_hour_resets_at} />
          <UsageBar label="Claude weekly" percent={usage.weekly_percent} resetsAt={usage.weekly_resets_at} />
        </div>
      )}
    </Card>
  )
}
