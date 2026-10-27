import type { NeedsMeItem } from '../../api/client'
import RiskFlagChips from '../shared/RiskFlagChips'
import { Badge, type BadgeTone } from '../ui'

const VERIFY: Record<string, { label: string; tone: BadgeTone }> = {
  pass: { label: 'Verified', tone: 'success' },
  fail: { label: 'Verify failed', tone: 'danger' },
  timeout: { label: 'Verify failed', tone: 'danger' },
  error: { label: "Verify couldn't run", tone: 'warning' },
  not_run: { label: 'Not verified', tone: 'neutral' },
}

/** Brief-derived context for a Decide row. Fields the server did not send are skipped. */
export default function DecisionContext({ item }: { item: NeedsMeItem }) {
  const ch = item.changed
  const v = item.verification ? (VERIFY[item.verification] ?? VERIFY.not_run) : undefined
  const hasMeta = ch !== undefined || item.cost_usd !== undefined || v !== undefined || (item.preview_count ?? 0) > 0
  return (
    <div data-testid="decision-context">
      {item.summary && (
        <p className="text-xs text-fg mt-1 truncate" title={item.summary}>{item.summary}</p>
      )}
      {hasMeta && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mt-1 text-xs text-fg-muted">
          {item.changed === undefined && item.verification !== undefined ? (
            <span>No diff yet</span>
          ) : ch ? (
            <span data-testid="decision-diff">
              <span className="text-success">+{ch.additions}</span> <span className="text-danger">−{ch.deletions}</span>
              {' · '}{ch.files} {ch.files === 1 ? 'file' : 'files'}
            </span>
          ) : null}
          {item.cost_usd !== undefined && (
            <span title={item.cost_unknown ? 'Some runs had unknown cost' : 'Cost so far'}>
              ${item.cost_usd.toFixed(2)}{item.cost_unknown ? '+?' : ''}
            </span>
          )}
          {v && <Badge tone={v.tone} size="xs">{v.label}</Badge>}
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
