import type { NeedsMeItem } from '../../api/client'
import RiskFlagChips from '../shared/RiskFlagChips'

const VERIFY: Record<string, { label: string; cls: string }> = {
  pass: { label: 'Verified', cls: 'bg-emerald-900/50 text-emerald-300 border-emerald-700' },
  fail: { label: 'Verify failed', cls: 'bg-red-900/50 text-red-300 border-red-700' },
  timeout: { label: 'Verify failed', cls: 'bg-red-900/50 text-red-300 border-red-700' },
  error: { label: "Verify couldn't run", cls: 'bg-amber-900/50 text-amber-300 border-amber-700' },
  not_run: { label: 'Not verified', cls: 'bg-slate-800 text-slate-400 border-slate-700' },
}

/** Brief-derived context for a Decide row. Fields the server did not send are skipped. */
export default function DecisionContext({ item }: { item: NeedsMeItem }) {
  const ch = item.changed
  const v = item.verification ? (VERIFY[item.verification] ?? VERIFY.not_run) : undefined
  const hasMeta = ch !== undefined || item.cost_usd !== undefined || v !== undefined || (item.preview_count ?? 0) > 0
  return (
    <div data-testid="decision-context">
      {item.summary && (
        <p className="text-xs text-slate-300 mt-1 truncate" title={item.summary}>{item.summary}</p>
      )}
      {hasMeta && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mt-1 text-xs text-slate-400">
          {item.changed === undefined && item.verification !== undefined ? (
            <span>No diff yet</span>
          ) : ch ? (
            <span data-testid="decision-diff">
              <span className="text-emerald-400">+{ch.additions}</span> <span className="text-red-400">−{ch.deletions}</span>
              {' · '}{ch.files} {ch.files === 1 ? 'file' : 'files'}
            </span>
          ) : null}
          {item.cost_usd !== undefined && (
            <span title={item.cost_unknown ? 'Some runs had unknown cost' : 'Cost so far'}>
              ${item.cost_usd.toFixed(2)}{item.cost_unknown ? '+?' : ''}
            </span>
          )}
          {v && <span className={`px-1.5 py-0.5 rounded border text-[10px] font-medium ${v.cls}`}>{v.label}</span>}
          {(item.preview_count ?? 0) > 0 && (
            <span>🖼 {item.preview_count} {item.preview_count === 1 ? 'preview' : 'previews'}</span>
          )}
        </div>
      )}
      {item.risk_flags && item.risk_flags.length > 0 && (
        <div className="mt-1">
          <RiskFlagChips codes={item.risk_flags} />
        </div>
      )}
    </div>
  )
}
