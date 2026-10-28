import { useState } from 'react'
import { X } from 'lucide-react'
import type { Workflow, WorkflowLabel } from '../../api/client'
import { backtestSummary, gateMode } from '../../lib/autonomyDial'
import { labelDisplayName } from '../../lib/labelDisplay'
import AutonomyDialChip from '../shared/AutonomyDialChip'
import ModalShell from '../shared/ModalShell'
import { Button, Icon } from '../ui'
import { GateFlagChecks, GateModeControl } from './GateControls'
import { useGateWarnings } from './useGateWarnings'
import { useGateDial } from './useGateDial'

/** Phone bottom sheet for one gate: mode, one-line backtest, flag count, Save. */
export default function GateDialSheet({
  workflow,
  label,
  onClose,
}: {
  workflow: Workflow
  label: WorkflowLabel
  onClose: () => void
}) {
  const g = useGateDial(workflow, label)
  const warnings = useGateWarnings(workflow, label, g.form.mode)
  const [editFlags, setEditFlags] = useState(false)
  const onFlags = g.form.mode === 'on_flags'

  return (
    <ModalShell
      onClose={onClose}
      ariaLabel={`Gate settings: ${labelDisplayName(label)}`}
      className="self-end w-full max-h-[85vh] overflow-y-auto rounded-t-xl border-t border-border bg-surface p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
    >
      <div className="flex items-center justify-between mb-3">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-base font-semibold text-fg">{labelDisplayName(label)}</h2>
          <AutonomyDialChip mode={gateMode(label)} />
        </div>
        <Button size="sm" variant="ghost" onClick={onClose} aria-label="Close gate settings">
          <Icon icon={X} size="sm" />
        </Button>
      </div>
      <div className="space-y-3">
        <GateModeControl mode={g.form.mode} onChange={g.setMode} hasDiff={g.hasDiff} />
        <p className="text-sm text-fg-muted" aria-live="polite">
          {g.btError ?? (g.backtest ? backtestSummary(g.backtest) : 'Last 30 days: computing…')}
        </p>
        {g.hasDiff && (
          <div className="text-sm text-fg">
            Flags that ask: {g.form.ask_on.length} of 7 ·{' '}
            {onFlags && (
              <button
                type="button"
                className="text-accent-fg underline"
                aria-expanded={editFlags}
                onClick={() => setEditFlags((v) => !v)}
              >
                {editFlags ? 'Done' : 'Edit'}
              </button>
            )}
          </div>
        )}
        {g.hasDiff && onFlags && editFlags && (
          <GateFlagChecks askOn={g.form.ask_on} mode={g.form.mode} onToggle={g.toggleFlag} />
        )}
        {warnings.map((w) => (
          <p key={w.key} role="status" className="rounded border border-warning/40 bg-warning-soft p-2 text-sm text-warning">
            {w.node}
          </p>
        ))}
        {g.stale && (
          <p role="alert" className="text-sm text-warning">
            Changed on the server; reload? <Button size="sm" onClick={() => void g.reload()}>Reload</Button>
          </p>
        )}
        {g.saveError && <p role="alert" className="text-sm text-danger">{g.saveError}</p>}
        <div className="flex items-center gap-2">
          <Button variant="primary" disabled={!g.differs || g.saving} onClick={() => void g.save()}>
            {g.saving ? 'Saving…' : 'Save'}
          </Button>
          {g.saved && !g.differs && <span role="status" className="text-xs text-success">Saved</span>}
        </div>
      </div>
    </ModalShell>
  )
}
