import { X } from 'lucide-react'
import Icon from '../ui/Icon'
import { useEffect, useRef } from 'react'

export type ToastVariant = 'success' | 'error' | 'info'

const VARIANT_CLASS: Record<ToastVariant, string> = {
  success: 'bg-success-soft border-success/40 text-success',
  error: 'bg-danger-soft border-danger/40 text-danger',
  info: 'bg-surface border-border text-fg',
}

/**
 * Minimal auto-dismissing toast, fixed above the mobile bottom tab bar.
 * An optional `action` (e.g. Undo) renders a button; the default timeout is
 * longer (6s) when an action is present.
 */
export default function Toast({
  message,
  onDismiss,
  timeoutMs,
  action,
  variant = 'success',
}: {
  message: string
  onDismiss: () => void
  timeoutMs?: number
  action?: { label: string; onClick: () => void }
  variant?: ToastVariant
}) {
  const dismissRef = useRef(onDismiss)
  dismissRef.current = onDismiss
  const ms = timeoutMs ?? (action ? 6000 : 4000)
  useEffect(() => {
    const t = setTimeout(() => dismissRef.current(), ms)
    return () => clearTimeout(t)
  }, [message, ms])

  const isError = variant === 'error'
  return (
    <div
      role={isError ? 'alert' : 'status'}
      aria-live={isError ? 'assertive' : 'polite'}
      className={`fixed bottom-20 md:bottom-6 left-1/2 -translate-x-1/2 z-50 max-w-[90vw] flex items-center gap-3 rounded-lg border text-sm px-4 py-2 shadow-lg ${VARIANT_CLASS[variant]}`}
    >
      <span className="break-words">{message}</span>
      {action && (
        <button
          onClick={() => {
            action.onClick()
            onDismiss()
          }}
          className="font-semibold underline hover:text-fg"
        >
          {action.label}
        </button>
      )}
      <button onClick={onDismiss} aria-label="Dismiss" className="hover:text-fg">
        <Icon icon={X} size="sm" />
      </button>
    </div>
  )
}
