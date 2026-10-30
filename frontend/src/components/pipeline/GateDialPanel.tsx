import { useEffect, useRef, useState } from 'react'
import { Lock, X } from 'lucide-react'
import type { Workflow, WorkflowLabel } from '../../api/client'
import { HOLD_OPTIONS, formatHold, gateMode } from '../../lib/autonomyDial'
import { labelDisplayName } from '../../lib/labelDisplay'
import AutonomyDialChip from '../shared/AutonomyDialChip'
import { Badge, Button, Field, Icon, controlClasses } from '../ui'
import { GateFlagChecks, GateModeControl } from './GateControls'
import GateBacktestCard from './GateBacktestCard'
import GateAskRules from './GateAskRules'
import GateRuleProposals from './GateRuleProposals'
import GateHistory from './GateHistory'
import { useGateDial } from './useGateDial'
import { useGateWarnings } from './useGateWarnings'
import { formFromLabel } from '../../lib/autonomyDial'

/** Autonomy settings for one gate, shown under the Pipeline graph. */
export default function GateDialPanel({
  workflow,
  label,
  onClose,
  onSaved,
}: {
  workflow: Workflow
  label: WorkflowLabel
  onClose: () => void
  onSaved?: () => void
}) {
  const g = useGateDial(workflow, label, onSaved)
  const headingRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => { headingRef.current?.focus() }, [label.name])
  const warnings = useGateWarnings(workflow, label, g.form.mode)
  const askMode = g.form.mode === 'ask'
  const holdKnown = HOLD_OPTIONS.some((o) => o.seconds === g.form.hold_seconds)
  const [rulesKey, setRulesKey] = useState(0)

  return (
    <div className="p-3 space-y-4">
      <div className="flex items-start justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h2 ref={headingRef} tabIndex={-1} className="text-base font-semibold text-fg focus:outline-none">
            {labelDisplayName(label)}
          </h2>
          <AutonomyDialChip mode={gateMode(label)} />
        </div>
        <button type="button" aria-label="Close gate panel" onClick={onClose} className="text-fg-muted hover:text-fg">
          <Icon icon={X} size="md" />
        </button>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-4 min-w-0">
          <GateModeControl mode={g.form.mode} onChange={g.setMode} hasDiff={g.hasDiff} />
          {g.hasDiff && <GateFlagChecks askOn={g.form.ask_on} mode={g.form.mode} onToggle={g.toggleFlag} />}

          <fieldset className="space-y-2 min-w-0">
            <legend className="text-xs text-fg-muted mb-1">Guardrails</legend>
            <div className="flex flex-wrap gap-2">
              <Badge title="Always applies">
                <Icon icon={Lock} size="xs" />Verify must pass
              </Badge>
              <Badge title="Always applies">
                <Icon icon={Lock} size="xs" />Breaker stops
              </Badge>
            </div>
            <div className="flex flex-wrap gap-3">
              <Field label="Hold before approving" className="w-40">
                <select
                  className={controlClasses}
                  disabled={askMode}
                  value={g.form.hold_seconds}
                  onChange={(e) => g.patch({ hold_seconds: Number(e.target.value) })}
                >
                  {HOLD_OPTIONS.map((o) => <option key={o.seconds} value={o.seconds}>{o.label}</option>)}
                  {!holdKnown && <option value={g.form.hold_seconds}>{formatHold(g.form.hold_seconds)}</option>}
                </select>
              </Field>
              <Field label="Max cost (USD)" className="w-40">
                <input
                  type="number"
                  min={0}
                  step={0.01}
                  className={controlClasses}
                  disabled={askMode}
                  value={g.form.max_cost_usd ?? ''}
                  onChange={(e) => g.patch({ max_cost_usd: e.target.value === '' ? null : Number(e.target.value) })}
                />
              </Field>
            </div>
          </fieldset>

          {warnings.map((w) => (
            <p key={w.key} role="status" className="rounded border border-warning/40 bg-warning-soft p-2 text-sm text-warning">
              {w.node}
            </p>
          ))}

          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" size="sm" disabled={!g.differs || g.saving} onClick={() => void g.save()}>
              {g.saving ? 'Saving…' : 'Save'}
            </Button>
            <Button size="sm" disabled={!g.differs || g.saving} onClick={g.reset}>Reset</Button>
            {g.saved && !g.differs && <span role="status" className="text-xs text-success">Saved</span>}
          </div>
          {g.stale && (
            <p role="alert" className="text-sm text-warning flex flex-wrap items-center gap-2">
              Changed on the server; reload?
              <Button size="sm" onClick={() => void g.reload()}>Reload</Button>
            </p>
          )}
          {g.saveError && <p role="alert" className="text-sm text-danger">{g.saveError}</p>}
        </div>

        <div className="space-y-4 min-w-0">
          <GateBacktestCard backtest={g.backtest} loading={g.btLoading} error={g.btError} />
          <GateAskRules workflowId={workflow.id} label={label.name} refreshKey={rulesKey} />
          <GateRuleProposals workflowId={workflow.id} label={label.name} onRuleAdded={() => setRulesKey((k) => k + 1)} />
          <GateHistory
            workflowId={workflow.id}
            label={label.name}
            currentMode={gateMode(label)}
            refreshKey={workflow.updated_at ?? ''}
            busy={g.saving}
            onChangeBack={(mode) => void g.saveForm({ ...formFromLabel(label), mode })}
          />
        </div>
      </div>
    </div>
  )
}
