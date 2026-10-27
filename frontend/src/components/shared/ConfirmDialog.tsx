import { useRef, useState, type ReactNode } from 'react'
import ModalShell from './ModalShell'
import Button from '../ui/Button'
import { errMessage } from '../../lib/errMessage'

/**
 * In-page replacement for window.confirm. Body should name exactly what will
 * be lost. If `onConfirm` throws, the message is shown inline and the dialog
 * stays open; on success the caller closes it. `requireText` adds a typed
 * confirmation.
 */
export default function ConfirmDialog({
  title,
  children,
  confirmLabel,
  cancelLabel = 'Cancel',
  destructive = false,
  requireText,
  onConfirm,
  onCancel,
}: {
  title: string
  children: ReactNode
  confirmLabel: string
  cancelLabel?: string
  destructive?: boolean
  requireText?: string
  onConfirm: () => void | Promise<void>
  onCancel: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [typed, setTyped] = useState('')
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const initialFocusRef = requireText ? inputRef : cancelRef
  const canConfirm = !busy && (requireText === undefined || typed === requireText)

  async function submit() {
    if (!canConfirm) return
    setBusy(true)
    setError(null)
    try {
      await onConfirm()
    } catch (e) {
      setError(errMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <ModalShell
      onClose={busy ? () => {} : onCancel}
      ariaLabel={title}
      initialFocusRef={initialFocusRef}
      className="bg-surface-raised border border-border rounded-lg shadow-xl w-full max-w-md mx-4 flex flex-col"
    >
      <div className="px-6 pt-5 pb-2">
        <h2 className="text-lg font-semibold text-fg">{title}</h2>
      </div>
      <div className="px-6 py-2 text-sm text-fg-muted flex flex-col gap-3">
        {children}
        {requireText !== undefined && (
          <label className="flex flex-col gap-1 text-xs text-fg-muted">
            <span>
              Type <strong className="text-fg">{requireText}</strong> to confirm
            </span>
            <input
              ref={inputRef}
              aria-label={`Type ${requireText} to confirm`}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  void submit()
                }
              }}
              className="bg-surface-sunken border border-border rounded px-2 py-1.5 text-sm text-fg"
            />
          </label>
        )}
        {error && (
          <p role="alert" className="text-xs text-danger">
            {error}
          </p>
        )}
      </div>
      <div className="px-6 py-4 flex justify-end gap-2">
        <Button ref={cancelRef} onClick={onCancel} disabled={busy}>
          {cancelLabel}
        </Button>
        <Button variant={destructive ? 'danger' : 'primary'} onClick={() => void submit()} disabled={!canConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </ModalShell>
  )
}
