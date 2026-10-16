// Minimal service worker. Its only job is to make browser notifications work
// on mobile: Android Chrome throws on `new Notification()` from a page and
// iOS home-screen apps only show notifications through a service worker, so
// src/lib/notify.ts calls registration.showNotification() instead. No fetch
// handler and no caching — the app still loads straight from the network.

self.addEventListener('install', () => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

// Tapping a notification focuses an open app window on the task (or opens a
// new one when none is open).
self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = new URL(event.notification.data?.url || './', self.registration.scope).href
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      for (const client of windows) {
        if (client.url.startsWith(self.registration.scope) && 'focus' in client) {
          await client.focus()
          if ('navigate' in client) await client.navigate(url)
          return
        }
      }
      await self.clients.openWindow(url)
    })(),
  )
})
