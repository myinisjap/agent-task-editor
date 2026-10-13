import { useEffect } from 'react'
import { wsClient } from '../api/ws'
import { useInboxStore } from '../stores/inbox'
import { useHealthStore } from '../stores/health'
import { useDebouncedCallback } from './useDebouncedCallback'

const WS_REFRESH_DEBOUNCE_MS = 250

/**
 * Keeps the non-needs-me parts of the inbox fresh: failed side effects
 * (refetched on task.side_effect / task.updated) and paused providers
 * (refetched on provider.pause_changed). Needs-me itself is kept by
 * useNeedsMeSync. Mounted once, in the nav.
 */
export function useInboxSync() {
  const refreshEffects = useInboxStore((s) => s.refresh)
  const fetchHealth = useHealthStore((s) => s.fetch)
  const debouncedEffects = useDebouncedCallback(() => { void refreshEffects() }, WS_REFRESH_DEBOUNCE_MS)

  useEffect(() => {
    void refreshEffects()
    void fetchHealth()
  }, [refreshEffects, fetchHealth])

  useEffect(() => {
    return wsClient.on((event) => {
      if (event.type === 'task.side_effect' || event.type === 'task.updated') debouncedEffects()
      else if (event.type === 'provider.pause_changed') void fetchHealth()
    })
  }, [debouncedEffects, fetchHealth])
}
