import type { AutonomyBacktest } from '../../api/client'
import { CATEGORY_NAME, backtestVerdict, passCategory, type PassCategory } from '../../lib/autonomyDial'
import { cx } from '../ui'

const CELL: Record<PassCategory, string> = {
  asks: 'bg-info',
  auto: 'bg-success',
  caught: 'bg-warning',
  slipped: 'bg-danger',
  unknown: 'bg-surface-sunken border border-border',
}

function Stat({ value, label, danger }: { value: number; label: string; danger?: boolean }) {
  return (
    <div className="min-w-0">
      <div className={cx('text-2xl font-semibold tabular-nums', danger ? 'text-danger' : 'text-fg')}>{value}</div>
      <div className="text-xs text-fg-muted">{label}</div>
    </div>
  )
}

/** Last-30-days backtest: three numbers, a one-cell-per-pass strip, legend and verdict. */
export default function GateBacktestCard({
  backtest,
  loading,
  error,
}: {
  backtest: AutonomyBacktest | null
  loading: boolean
  error: string | null
}) {
  const cats: PassCategory[] = ['asks', 'auto', 'caught', 'slipped']
  const hasUnknown = backtest?.passes.some((p) => passCategory(p) === 'unknown')
  return (
    <section aria-label="Backtest, last 30 days" className="rounded border border-border p-3 space-y-2">
      <h3 className="text-sm font-semibold text-fg">
        Backtest, last 30 days {loading && <span className="text-xs font-normal text-fg-muted">Recomputing…</span>}
      </h3>
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      {backtest && (
        <>
          <div className="grid grid-cols-3 gap-3">
            <Stat value={backtest.would_skip} label="Would skip" />
            <Stat value={backtest.would_ask} label="Would ask" />
            <Stat value={backtest.escaped} label="Rejections missed" danger={backtest.escaped > 0} />
          </div>
          {backtest.passes.length > 0 && (
            <ol aria-label="Passes, oldest first" className="flex flex-wrap gap-0.5">
              {backtest.passes.map((p, i) => {
                const c = passCategory(p)
                const text = `${p.title}, ${new Date(p.at).toLocaleDateString()}, ${CATEGORY_NAME[c]}`
                return (
                  <li key={`${p.task_id}-${i}`} title={text} className={cx('h-4 w-2 rounded-sm', CELL[c])}>
                    <span className="sr-only">{text}</span>
                  </li>
                )
              })}
            </ol>
          )}
          <ul aria-label="Legend" className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-fg-muted">
            {[...cats, ...(hasUnknown ? (['unknown'] as PassCategory[]) : [])].map((c) => (
              <li key={c} className="flex items-center gap-1">
                <span aria-hidden className={cx('inline-block h-3 w-2 rounded-sm', CELL[c])} />
                {CATEGORY_NAME[c]}
              </li>
            ))}
          </ul>
          <p className="text-sm text-fg">{backtestVerdict(backtest)}</p>
        </>
      )}
      {!backtest && !error && <p className="text-xs text-fg-muted">Computing…</p>}
    </section>
  )
}
