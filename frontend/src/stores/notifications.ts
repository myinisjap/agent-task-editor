import { create } from 'zustand'

const STORAGE_KEY = 'notifications.enabled'

/** Feature-detect the Notifications API (absent on some browsers, e.g. iOS
 *  Safari outside an installed PWA). Guard every access behind this. */
export function notificationsSupported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window
}

function initialEnabled(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

function currentPermission(): NotificationPermission {
  if (!notificationsSupported()) return 'default'
  try {
    return Notification.permission
  } catch {
    return 'default'
  }
}

interface NotificationsState {
  /** User's opt-in preference, persisted. Default off — enabling requires an
   *  explicit user gesture (see requestPermission). */
  enabled: boolean
  /** Mirrors Notification.permission ('default' | 'granted' | 'denied'). */
  permission: NotificationPermission
  setEnabled: (enabled: boolean) => void
  toggle: () => void
  /** Must be called from a user gesture (click handler). Requests browser
   *  permission; only leaves `enabled` true if permission is granted. */
  requestPermission: () => Promise<void>
}

export const useNotificationsStore = create<NotificationsState>((set, get) => ({
  enabled: initialEnabled(),
  permission: currentPermission(),

  setEnabled: (enabled) => {
    try { localStorage.setItem(STORAGE_KEY, enabled ? '1' : '0') } catch { /* ignore */ }
    set({ enabled })
  },

  toggle: () => {
    const { enabled } = get()
    if (enabled) {
      get().setEnabled(false)
      return
    }
    void get().requestPermission()
  },

  requestPermission: async () => {
    if (!notificationsSupported()) return
    try {
      const permission = await Notification.requestPermission()
      set({ permission })
      if (permission === 'granted') {
        get().setEnabled(true)
      } else {
        get().setEnabled(false)
      }
    } catch {
      // ignore — leave state unchanged
    }
  },
}))

/** True when notifications should actually be fired: supported, opted in,
 *  and permission has been granted. */
export function notificationsActive(): boolean {
  if (!notificationsSupported()) return false
  const { enabled, permission } = useNotificationsStore.getState()
  return enabled && permission === 'granted'
}
