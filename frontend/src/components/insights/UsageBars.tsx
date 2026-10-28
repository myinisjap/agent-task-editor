/**
 * A single daily/monthly global-spend-ceiling row: spend vs. cap as a
 * progress bar, plus the projected end-of-period total (see
 * globalCostBudgetView.daily_forecast_usd/monthly_forecast_usd on the
 * backend — a simple trailing-burn-rate extrapolation, not a prediction).
 */
export function CostBudgetBar({
  label,
  spent,
  limit,
  forecast,
  showForecastMarker = false,
}: {
  label: string
  spent: number
  limit: number
  forecast?: number | null
  /** Draw a tick on the bar at the projected total (used by Home). */
  showForecastMarker?: boolean
}) {
  const percent = limit > 0 ? Math.min(100, Math.max(0, (spent / limit) * 100)) : 0
  const barColor = percent >= 100 ? 'bg-red-500' : percent >= 80 ? 'bg-amber-500' : 'bg-emerald-500'
  const textColor = percent >= 100 ? 'text-danger' : percent >= 80 ? 'text-warning' : 'text-fg-muted'

  return (
    <div>
      <div className="flex items-center justify-between mb-1.5">
        <span className="text-sm text-fg-muted">{label}</span>
        <span className={`text-sm font-semibold ${textColor}`}>
          ${spent.toFixed(2)} / ${limit.toFixed(2)}
        </span>
      </div>
      <div className="relative w-full h-2 rounded-full bg-surface-sunken overflow-hidden">
        <div className={`h-full rounded-full ${barColor}`} style={{ width: `${percent}%` }} />
        {showForecastMarker && typeof forecast === 'number' && limit > 0 && (
          <div
            data-testid="forecast-marker"
            title={`Projected ${forecast.toFixed(2)}`}
            className="absolute top-0 bottom-0 w-0.5 bg-fg"
            style={{ left: `calc(${Math.min(100, Math.max(0, (forecast / limit) * 100))}% - 1px)` }}
          />
        )}
      </div>
      {typeof forecast === 'number' && (
        <div className="text-xs text-fg-subtle mt-1">
          Projected at current burn: ${forecast.toFixed(2)}
          {forecast > limit ? ' (on track to exceed the cap)' : ''}
        </div>
      )}
    </div>
  )
}

/** A single 5h/weekly Claude usage row: label, percentage, progress bar, reset time. */
export function UsageBar({ label, percent, resetsAt }: { label: string; percent: number; resetsAt?: string | null }) {
  const clamped = Math.min(100, Math.max(0, percent))
  const barColor = clamped >= 95 ? 'bg-red-500' : clamped >= 80 ? 'bg-amber-500' : 'bg-emerald-500'
  const textColor = clamped >= 95 ? 'text-danger' : clamped >= 80 ? 'text-warning' : 'text-fg-muted'

  return (
    <div>
      <div className="flex items-center justify-between mb-1.5">
        <span className="text-sm text-fg-muted">{label}</span>
        <span className={`text-sm font-semibold ${textColor}`}>{clamped.toFixed(0)}%</span>
      </div>
      <div className="w-full h-2 rounded-full bg-surface-sunken overflow-hidden">
        <div
          className={`h-full rounded-full ${barColor}`}
          style={{ width: `${clamped}%` }}
        />
      </div>
      {resetsAt && (
        <div className="text-xs text-fg-subtle mt-1">
          Resets {new Date(resetsAt).toLocaleString()}
        </div>
      )}
    </div>
  )
}
