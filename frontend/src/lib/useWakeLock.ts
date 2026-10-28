import { useEffect } from 'react'

type Sentinel = { release: () => Promise<void> }

/** Keeps the screen awake while the page is visible. Failures are ignored. */
export function useWakeLock(enabled = true) {
  useEffect(() => {
    const wl = (navigator as unknown as { wakeLock?: { request: (t: 'screen') => Promise<Sentinel> } }).wakeLock
    if (!enabled || !wl) return
    let sentinel: Sentinel | null = null
    let dead = false
    const release = () => {
      const s = sentinel
      sentinel = null
      s?.release().catch(() => {})
    }
    const acquire = () => {
      if (document.visibilityState !== 'visible' || sentinel) return
      Promise.resolve(wl.request('screen'))
        .then((s) => {
          if (dead) s.release().catch(() => {})
          else sentinel = s
        })
        .catch(() => {})
    }
    const onVis = () => (document.visibilityState === 'visible' ? acquire() : release())
    document.addEventListener('visibilitychange', onVis)
    acquire()
    return () => {
      dead = true
      document.removeEventListener('visibilitychange', onVis)
      release()
    }
  }, [enabled])
}
