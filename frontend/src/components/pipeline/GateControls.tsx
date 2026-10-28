import { useId } from 'react'
import type { RiskFlagCode } from '../../api/client'
import { DIAL_MODES, DIAL_NAME, type DialMode } from '../../lib/autonomyDial'
import { RISK_FLAG_CODES, riskFlagLabel } from '../../lib/riskFlags'
import { cx } from '../ui'

export const NO_DIFF_NOTE =
  'Tasks have no code changes yet at this gate, so Ask on flags would always ask here.'

/** Always ask / Ask on flags / Auto as native radios styled as segments. */
export function GateModeControl({
  mode,
  onChange,
  hasDiff,
}: {
  mode: DialMode
  onChange: (m: DialMode) => void
  hasDiff: boolean
}) {
  const name = useId()
  // Never hide the option when it is the current value: do not change saved state silently.
  const modes = DIAL_MODES.filter((m) => m !== 'on_flags' || hasDiff || mode === 'on_flags')
  return (
    <fieldset className="min-w-0">
      <legend className="text-xs text-fg-muted mb-1">Gate mode</legend>
      <div className="inline-flex rounded border border-border overflow-hidden">
        {modes.map((m) => (
          <label
            key={m}
            className={cx(
              'px-3 py-2 text-sm cursor-pointer border-l border-border first:border-l-0 focus-within:ring-2 focus-within:ring-accent',
              mode === m ? 'bg-accent text-on-accent' : 'bg-surface-sunken text-fg hover:bg-surface-raised',
            )}
          >
            <input
              type="radio"
              name={name}
              value={m}
              checked={mode === m}
              onChange={() => onChange(m)}
              className="sr-only"
            />
            {DIAL_NAME[m]}
          </label>
        ))}
      </div>
      {!hasDiff && <p className="text-xs text-fg-muted mt-1">{NO_DIFF_NOTE}</p>}
    </fieldset>
  )
}

/** The seven brief-flag checkboxes; enabled only for Ask on flags. */
export function GateFlagChecks({
  askOn,
  mode,
  onToggle,
}: {
  askOn: RiskFlagCode[]
  mode: DialMode
  onToggle: (c: RiskFlagCode) => void
}) {
  const enabled = mode === 'on_flags'
  return (
    <fieldset disabled={!enabled} className="min-w-0">
      <legend className="text-xs text-fg-muted mb-1">Ask when the brief flags</legend>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-1">
        {RISK_FLAG_CODES.map((c) => (
          <label key={c} className={cx('flex items-center gap-2 text-sm', enabled ? 'text-fg' : 'text-fg-subtle')}>
            <input type="checkbox" checked={askOn.includes(c)} onChange={() => onToggle(c)} />
            {riskFlagLabel(c)}
          </label>
        ))}
      </div>
    </fieldset>
  )
}
