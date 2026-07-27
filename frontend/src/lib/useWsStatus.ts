import { useEffect, useState } from 'react'
import { wsClient, type WSStatus } from '../api/ws'

/**
 * Subscribes to the shared WebSocket client's connection status so any
 * component can render a live "connected / reconnecting / offline"
 * indicator. Safe to use even when the app never establishes a WS
 * connection (e.g. open-auth misconfiguration) — it just reflects whatever
 * status wsClient reports, defaulting to 'closed' before the first change.
 */
export function useWsStatus(): WSStatus {
  const [status, setStatus] = useState<WSStatus>(() => wsClient.getStatus())

  useEffect(() => {
    // Re-sync in case status changed between initial render and mount.
    setStatus(wsClient.getStatus())
    return wsClient.onStatusChange(setStatus)
  }, [])

  return status
}
