import { useEffect, useRef } from 'react'

/** Minimal auto-dismissing toast, fixed above the mobile bottom tab bar. */
export default function Toast({
  message,
  onDismiss,
  timeoutMs = 4000,
}: {
  message: string
  onDismiss: () => void
  timeoutMs?: number
}) {
  const dismissRef = useRef(onDismiss)
  dismissRef.current = onDismiss
  useEffect(() => {
    const t = setTimeout(() => dismissRef.current(), timeoutMs)
    return () => clearTimeout(t)
  }, [message, timeoutMs])

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed bottom-20 md:bottom-6 left-1/2 -translate-x-1/2 z-50 max-w-[90vw] flex items-center gap-3 rounded-lg bg-emerald-900 border border-emerald-700 text-emerald-100 text-sm px-4 py-2 shadow-lg"
    >
      <span className="break-words">{message}</span>
      <button onClick={onDismiss} aria-label="Dismiss" className="text-emerald-300 hover:text-white">
        ×
      </button>
    </div>
  )
}
