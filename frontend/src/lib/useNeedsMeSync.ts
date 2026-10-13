import { useEffect } from 'react'
import { wsClient } from '../api/ws'
import { useNeedsMeStore } from '../stores/needsMe'
import { useDebouncedCallback } from './useDebouncedCallback'

const WS_REFRESH_DEBOUNCE_MS = 250

/**
 * Keeps the needs-me store fresh: fetches on mount and re-fetches (debounced)
 * on task-level WS events that can change the set. Mounted once, in the nav.
 */
export function useNeedsMeSync() {
  const refresh = useNeedsMeStore((s) => s.refresh)
  const debounced = useDebouncedCallback(() => { void refresh() }, WS_REFRESH_DEBOUNCE_MS)

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    return wsClient.on((event) => {
      if (
        event.type === 'task.label_changed' ||
        event.type === 'task.agent_started' ||
        event.type === 'task.agent_done' ||
        event.type === 'task.needs_human' ||
        event.type === 'task.updated' ||
        event.type === 'task.git_state_changed' ||
        event.type === 'task.pr_mergeable_changed'
      ) {
        debounced()
      }
    })
  }, [debounced])
}
