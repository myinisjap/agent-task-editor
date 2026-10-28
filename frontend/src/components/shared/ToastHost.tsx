import Toast from './Toast'
import { useToastStore } from '../../stores/toast'

/** Mounted once: renders the global toast from the toast store. */
export default function ToastHost() {
  const toast = useToastStore((s) => s.toast)
  const dismiss = useToastStore((s) => s.dismiss)
  if (!toast) return null
  return (
    <Toast
      key={toast.id}
      message={toast.message}
      variant={toast.variant}
      action={toast.action}
      timeoutMs={toast.timeoutMs}
      onDismiss={() => dismiss(toast.id)}
    />
  )
}
