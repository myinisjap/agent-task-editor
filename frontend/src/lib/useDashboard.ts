import { useCallback, useEffect, useState } from 'react'
import { api, type Dashboard } from '../api/client'
import { wsClient } from '../api/ws'
import { useDebouncedCallback } from './useDebouncedCallback'

// A burst of task.label_changed/agent_started/agent_done/needs_human events
// (e.g. several agents finishing around the same time, or a bulk board
// action) would otherwise each trigger their own GET /dashboard round-trip.
// Coalesce them behind a short trailing debounce.
const WS_REFRESH_DEBOUNCE_MS = 250

/**
 * Fetches the shared `GET /dashboard` payload on mount and re-fetches it
 * whenever a task-level WS event arrives (label change, agent start/done,
 * or a new needs-human escalation). Used by the Overview, Cost & Usage,
 * and Agent Performance pages, which all render different slices of the
 * same `Dashboard` object.
 */
export function useDashboard() {
  const [dash, setDash] = useState<Dashboard | null>(null)

  const refresh = useCallback(() => {
    api.dashboard.get().then(setDash).catch(() => {})
  }, [])

  const debouncedRefresh = useDebouncedCallback(refresh, WS_REFRESH_DEBOUNCE_MS)

  useEffect(() => {
    refresh()
  }, [refresh])

  useEffect(() => {
    return wsClient.on((event) => {
      if (
        event.type === 'task.label_changed' ||
        event.type === 'task.agent_started' ||
        event.type === 'task.agent_done' ||
        event.type === 'task.needs_human'
      ) {
        debouncedRefresh()
      }
    })
  }, [debouncedRefresh])

  return { dash, refresh }
}
