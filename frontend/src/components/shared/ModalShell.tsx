import { useEffect, useRef, type MutableRefObject, type ReactNode } from 'react'

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Reusable dialog shell providing the fixed-overlay backdrop, dialog ARIA
 * semantics (`role="dialog"`, `aria-modal`, `aria-label`), Escape-to-close,
 * backdrop-click-to-close, and a focus trap that:
 *  - focuses `initialFocusRef` (or the first focusable element) on open,
 *  - keeps Tab/Shift+Tab cycling within the dialog while it's open,
 *  - restores focus to whatever was focused before the dialog opened, once
 *    it closes.
 *
 * Presentational only — callers own open/close state and supply their own
 * content (typically a header with a title/close button, plus a body).
 */
export default function ModalShell({
  onClose,
  ariaLabel,
  children,
  className,
  initialFocusRef,
}: {
  onClose: () => void
  ariaLabel: string
  children: ReactNode
  /** Extra classes for the dialog card (the backdrop itself is fixed). */
  className?: string
  /** Element to focus when the dialog opens; falls back to the first focusable element. */
  initialFocusRef?: MutableRefObject<HTMLElement | null>
}) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const previouslyFocusedRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    previouslyFocusedRef.current = document.activeElement as HTMLElement | null

    const focusTarget = initialFocusRef?.current ?? dialogRef.current?.querySelector<HTMLElement>(FOCUSABLE_SELECTOR)
    focusTarget?.focus()

    return () => {
      previouslyFocusedRef.current?.focus?.()
    }
    // Only run on mount/unmount — re-focusing on every render (e.g. as
    // initialFocusRef's target mounts later) isn't the intent here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      if (e.key !== 'Tab') return

      const container = dialogRef.current
      if (!container) return
      // Note: deliberately not filtering on `offsetParent`/visibility here —
      // it's unreliable in test environments (jsdom never computes layout,
      // so offsetParent is always null) and callers within this codebase
      // already keep hover-only controls out of the tab order via
      // `tabIndex={-1}` (see TaskCard) rather than relying on the trap to
      // skip them.
      const focusable = Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
      if (focusable.length === 0) return

      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      const active = document.activeElement

      if (e.shiftKey) {
        if (active === first || !container.contains(active)) {
          e.preventDefault()
          last.focus()
        }
      } else {
        if (active === last || !container.contains(active)) {
          e.preventDefault()
          first.focus()
        }
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  function handleBackdropClick(e: React.MouseEvent) {
    if (e.target === e.currentTarget) onClose()
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={handleBackdropClick}
      role="dialog"
      aria-modal="true"
      aria-label={ariaLabel}
      ref={dialogRef}
    >
      <div onClick={(e) => e.stopPropagation()} className={className}>
        {children}
      </div>
    </div>
  )
}
