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

/**
 * Shows a browser notification that a task needs human attention. No-ops
 * silently when the Notifications API is unsupported, the user hasn't
 * opted in, or permission isn't granted — see stores/notifications.ts.
 */
export function showHumanNeededNotification({ title, body, taskId }: HumanNeededNotificationOptions): void {
  if (!notificationsSupported()) return
  if (!notificationsActive()) return
  if (shouldSkipDuplicate(taskId)) return

  try {
    const notification = new Notification(title, {
      body,
      tag: `human-needed-${taskId}`,
      icon: ICON,
    })
    notification.onclick = () => {
      window.focus()
      const base = import.meta.env.BASE_URL.replace(/\/$/, '')
      window.location.assign(`${base}/tasks/${taskId}`)
    }
  } catch {
    // Constructing a Notification can throw in some environments (e.g. some
    // mobile browsers require a service worker registration); degrade
    // silently rather than surface an error to the user.
  }
}
