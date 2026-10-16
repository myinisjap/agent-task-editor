import { create } from 'zustand'

const STORAGE_KEY = 'notifications.enabled'

/** Feature-detect the Notifications API (absent on some browsers, e.g. iOS
 *  Safari outside an installed PWA). Guard every access behind this. */
export function notificationsSupported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window
}

/** Why notifications can't be offered on this device, or null when they can.
 *  Shown in the sidebar instead of silently hiding the toggle.
 *  - 'insecure': the page isn't served over HTTPS (or localhost); browsers
 *    only allow notifications in secure contexts, e.g. not over
 *    http://<lan-ip>:5173 from a phone.
 *  - 'ios-install': iOS/iPadOS Safari only offers notifications to a web app
 *    added to the Home Screen (iOS 16.4+).
 *  - 'unsupported': the browser has no Notifications API at all. */
export type NotificationsUnavailableReason = 'insecure' | 'ios-install' | 'unsupported'

function isIOS(): boolean {
  if (typeof navigator === 'undefined') return false
  const ua = navigator.userAgent
  // iPadOS 13+ reports itself as a Mac; tell it apart by touch support.
  return /iPad|iPhone|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)
}

function isStandalone(): boolean {
  if (typeof window === 'undefined') return false
  const nav = navigator as Navigator & { standalone?: boolean }
  return nav.standalone === true || window.matchMedia?.('(display-mode: standalone)').matches === true
}

export function notificationsUnavailableReason(): NotificationsUnavailableReason | null {
  if (typeof window === 'undefined') return 'unsupported'
  if (window.isSecureContext === false) return 'insecure'
  if (notificationsSupported()) return null
  if (isIOS() && !isStandalone()) return 'ios-install'
  return 'unsupported'
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
