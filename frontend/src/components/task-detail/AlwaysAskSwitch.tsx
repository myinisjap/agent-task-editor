import { useId } from 'react'

// AlwaysAskSwitch toggles the per-task always_ask override.
export default function AlwaysAskSwitch({ checked, onChange, disabled }: { checked: boolean; onChange: (next: boolean) => void; disabled?: boolean }) {
  const labelId = useId()
  const hintId = useId()
  return (
    <div className="flex items-start gap-3" data-testid="always-ask-switch">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-labelledby={labelId}
        aria-describedby={hintId}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`mt-0.5 relative h-5 w-9 shrink-0 rounded-full border transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50 ${
          checked ? 'bg-accent border-accent' : 'bg-surface-sunken border-border'
        }`}
      >
        <span
          aria-hidden
          className={`absolute left-0.5 top-0.5 h-3.5 w-3.5 rounded-full transition-transform ${
            checked ? 'bg-on-accent translate-x-4' : 'bg-fg-muted'
          }`}
        />
      </button>
      <div className="min-w-0">
        <div id={labelId} className="text-xs font-semibold text-fg-muted">Always ask for this task</div>
        <div id={hintId} className="text-xs text-fg-subtle">Gates never auto-approve this task, whatever their autonomy setting.</div>
      </div>
    </div>
  )
}
