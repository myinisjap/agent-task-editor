/**
 * Registers public/sw.js, which mobile browsers need before they will show a
 * notification (see src/lib/notify.ts). Service workers only exist in secure
 * contexts (HTTPS or localhost), so this is a no-op over plain HTTP.
 */
export function registerServiceWorker(): void {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  if (typeof window !== 'undefined' && !window.isSecureContext) return
  const base = import.meta.env.BASE_URL
  navigator.serviceWorker.register(`${base}sw.js`, { scope: base }).catch(() => {
    // Not fatal: desktop browsers can still show notifications without it.
  })
}
