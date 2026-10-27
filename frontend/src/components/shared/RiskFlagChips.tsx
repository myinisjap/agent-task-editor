import { riskFlagClass, riskFlagLabel } from '../../lib/riskFlags'

// RiskFlagChips renders review-brief risk flags as small chips. `reasons`
// optionally maps a code to its explanation (shown as a tooltip).
export default function RiskFlagChips({ codes, reasons }: { codes: string[]; reasons?: Record<string, string> }) {
  return (
    <div className="flex flex-wrap gap-1 min-w-0" data-testid="risk-flag-chips">
      {codes.map((c) => (
        <span
          key={c}
          title={reasons?.[c]}
          data-testid={`risk-flag-${c}`}
          className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${riskFlagClass(c)}`}
        >
          {riskFlagLabel(c)}
        </span>
      ))}
    </div>
  )
}
