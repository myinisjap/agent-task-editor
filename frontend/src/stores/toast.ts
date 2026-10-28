import { create } from 'zustand'
import type { ToastVariant } from '../components/shared/Toast'
import { errMessage } from '../lib/errMessage'

export interface ToastData {
  id: number
  message: string
  variant?: ToastVariant
  action?: { label: string; onClick: () => void }
  timeoutMs?: number
}

interface ToastState {
  toast: ToastData | null
  show: (t: Omit<ToastData, 'id'>) => void
  /** Clears the toast; with an id, only if it is still the current one. */
  dismiss: (id?: number) => void
}

let nextId = 1

export const useToastStore = create<ToastState>((set) => ({
  toast: null,
  show: (t) => set({ toast: { ...t, id: nextId++ } }),
  dismiss: (id) => set((s) => (id === undefined || s.toast?.id === id ? { toast: null } : s)),
}))

export function showToast(message: string, opts: Omit<ToastData, 'id' | 'message'> = {}) {
  useToastStore.getState().show({ message, ...opts })
}

export function showError(message: string) {
  showToast(message, { variant: 'error' })
}

/** Toast with an Undo button that runs `undo`; failures surface as an error toast. */
export function showUndoToast(message: string, undo: () => Promise<unknown>) {
  showToast(message, {
    action: {
      label: 'Undo',
      onClick: () => {
        undo().then(
          () => showToast('Undone'),
          (e) => showError(`Undo failed: ${errMessage(e)}`),
        )
      },
    },
  })
}
