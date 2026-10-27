import { cloneElement, useId, type ReactElement, type ReactNode } from 'react'
import { cx } from './cx'

export const controlClasses =
  'w-full rounded border border-border bg-surface-sunken px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:outline-none focus:border-accent disabled:opacity-50'

export type FieldProps = {
  label: ReactNode
  hint?: ReactNode
  error?: ReactNode
  labelHidden?: boolean
  id?: string
  className?: string
  children: ReactElement<{ id?: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }>
}

export default function Field({ label, hint, error, labelHidden, id, className, children }: FieldProps) {
  const auto = useId()
  const controlId = children.props.id ?? id ?? auto
  const hintId = hint ? `${controlId}-hint` : undefined
  const errorId = error ? `${controlId}-error` : undefined
  const describedBy = [children.props['aria-describedby'], hintId, errorId].filter(Boolean).join(' ') || undefined
  const control = cloneElement(children, {
    id: controlId,
    'aria-describedby': describedBy,
    ...(error ? { 'aria-invalid': true } : {}),
  })
  return (
    <div className={className}>
      <label htmlFor={controlId} className={labelHidden ? 'sr-only' : 'text-xs text-fg-muted mb-1 block'}>
        {label}
      </label>
      {control}
      {hint && <p id={hintId} className="text-xs text-fg-subtle mt-1">{hint}</p>}
      {error && <p id={errorId} role="alert" className={cx('text-xs text-danger mt-1')}>{error}</p>}
    </div>
  )
}
