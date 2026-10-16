import { notificationsActive, notificationsSupported } from '../stores/notifications'

const ICON = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/icon-192.png`

// Short-TTL de-dupe: the same "needs human" situation can surface as both a
// task.needs_human event and a human-gate task.label_changed event in quick
// succession. Notification's `tag` option collapses same-tag notifications
// at the OS/browser level, but we also skip re-firing app-side within this
// window so we don't even construct a second Notification.
const DEDUPE_TTL_MS = 30_000
const recentlyNotified = new Map<string, number>()

function shouldSkipDuplicate(taskId: string): boolean {
  const now = Date.now()
  const last = recentlyNotified.get(taskId)
  // Opportunistically drop stale entries so the map doesn't grow unbounded.
  for (const [id, ts] of recentlyNotified) {
    if (now - ts > DEDUPE_TTL_MS) recentlyNotified.delete(id)
  }
  if (last !== undefined && now - last < DEDUPE_TTL_MS) return true
  recentlyNotified.set(taskId, now)
  return false
}

export interface HumanNeededNotificationOptions {
  title: string
  body: string
  taskId: string
}

/** The page's service worker registration, if one is active (see
 *  src/lib/serviceWorker.ts). Mobile browsers only show notifications through
 *  it: Android Chrome throws "Illegal constructor" on `new Notification()`. */
async function serviceWorkerRegistration(): Promise<ServiceWorkerRegistration | undefined> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return undefined
  try {
    return await navigator.serviceWorker.getRegistration()
  } catch {
    return undefined
  }
}

/**
 * Shows a browser notification that a task needs human attention. No-ops
 * silently when the Notifications API is unsupported, the user hasn't
 * opted in, or permission isn't granted — see stores/notifications.ts.
 * Goes through the service worker when there is one (required on mobile),
 * else falls back to the page-level Notification constructor.
 */
export async function showHumanNeededNotification({ title, body, taskId }: HumanNeededNotificationOptions): Promise<void> {
  if (!notificationsSupported()) return
  if (!notificationsActive()) return
  if (shouldSkipDuplicate(taskId)) return

  const base = import.meta.env.BASE_URL.replace(/\/$/, '')
  const url = `${window.location.origin}${base}/tasks/${taskId}`
  const options: NotificationOptions = {
    body,
    tag: `human-needed-${taskId}`,
    icon: ICON,
    data: { url },
  }

  const registration = await serviceWorkerRegistration()
  if (registration) {
    try {
      // Click handling lives in public/sw.js (notificationclick).
      await registration.showNotification(title, options)
      return
    } catch {
      // fall through to the page-level constructor
    }
  }

  try {
    const notification = new Notification(title, options)
    notification.onclick = () => {
      window.focus()
      window.location.assign(url)
    }
  } catch {
    // Constructing a Notification throws on mobile browsers without a service
    // worker; degrade silently rather than surface an error to the user.
  }
}
