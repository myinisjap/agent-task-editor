import { useCallback, useEffect, useState } from 'react'
import { api, type Dashboard } from '../api/client'
import { wsClient } from '../api/ws'

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
        refresh()
      }
    })
  }, [refresh])

  return { dash, refresh }
}
