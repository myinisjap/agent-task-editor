import type { components } from './types'
import { getApiToken, notifyUnauthorized } from './authToken'

// wsTicketParam returns a "?ticket=..." query string for authenticating a
// WebSocket handshake, or "" when no API token is configured (open auth) or the
// exchange fails. Browsers can't set the bearer header on a WS upgrade, so the
// token is exchanged for a short-lived single-use ticket via POST /ws-ticket
// (see ServeWS). Shared by the event WS (below) and the chat terminal WS.
export async function wsTicketParam(): Promise<string> {
  const token = getApiToken()
  if (!token) return ''
  const base = import.meta.env.BASE_URL.replace(/\/$/, '')
  try {
    const res = await fetch(`${base}/api/v1/ws-ticket`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.ok) {
      const { ticket } = await res.json()
      if (ticket) return `?ticket=${encodeURIComponent(ticket)}`
    } else if (res.status === 401) {
      notifyUnauthorized()
    }
  } catch {
    // Fall through with no ticket — the caller's connection will 401/retry.
  }
  return ''
}

// WSEvent is generated from the WsEvent schema in openapi.yaml (the source of
// truth for the WebSocket contract, mirrored by the Go structs in
// backend/internal/wsevent). Do not hand-write payload shapes here; edit the
// spec and run `npm run gen:api`. Per-event semantics are in docs/websocket.md.
export type WSEvent = components['schemas']['WsEvent']

type Handler = (event: WSEvent) => void

export type WSStatus = 'connecting' | 'open' | 'closed'
type StatusHandler = (status: WSStatus) => void

// Base reconnect delay and cap for the exponential-backoff-with-jitter
// schedule below (see onclose). Exported for tests.
export const RECONNECT_BASE_MS = 1000
export const RECONNECT_MAX_MS = 30000

class WSClient {
  private ws: WebSocket | null = null
  private handlers: Handler[] = []
  private subscriptions = new Set<string>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectAttempts = 0
  private statusHandlers: StatusHandler[] = []
  private status: WSStatus = 'closed'
  // True once a connection has opened at least once, so a later open is known
  // to be a reconnect (events may have been missed while down).
  private hasOpened = false

  private setStatus(status: WSStatus) {
    this.status = status
    this.statusHandlers.forEach((h) => h(status))
  }

  getStatus(): WSStatus {
    return this.status
  }

  async connect() {
    // Already connected or connecting — don't double-connect
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }

    this.setStatus('connecting')

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const base = import.meta.env.BASE_URL.replace(/\/$/, '')
    const ticketParam = await wsTicketParam()

    const url = `${proto}//${window.location.host}${base}/ws${ticketParam}`
    this.ws = new WebSocket(url)

    this.ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as WSEvent
        this.dispatch(event)
      } catch {
        // ignore malformed messages
      }
    }

    this.ws.onclose = () => {
      this.setStatus('closed')
      // Capped exponential backoff with full jitter: avoids every open tab
      // hammering /ws-ticket + /ws in lockstep every 3s on a server restart.
      const capped = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** this.reconnectAttempts)
      const delay = capped / 2 + Math.random() * (capped / 2)
      this.reconnectAttempts++
      this.reconnectTimer = setTimeout(() => this.connect(), delay)
    }

    this.ws.onopen = () => {
      if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
      this.reconnectAttempts = 0
      this.setStatus('open')
      // Re-subscribe on reconnect
      this.subscriptions.forEach((id) => this.subscribeTask(id))
      // Events published while we were disconnected are gone: tell consumers
      // to refetch, exactly as for a server-sent resync frame.
      if (this.hasOpened) this.dispatch({ type: 'resync', payload: {} })
      this.hasOpened = true
    }
  }

  private dispatch(event: WSEvent) {
    this.handlers.forEach((h) => h(event))
  }

  on(handler: Handler) {
    this.handlers.push(handler)
    return () => {
      this.handlers = this.handlers.filter((h) => h !== handler)
    }
  }

  onStatusChange(handler: StatusHandler) {
    this.statusHandlers.push(handler)
    return () => {
      this.statusHandlers = this.statusHandlers.filter((h) => h !== handler)
    }
  }

  subscribeTask(taskId: string) {
    this.subscriptions.add(taskId)
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'subscribe', task_id: taskId }))
    }
  }

  unsubscribeTask(taskId: string) {
    this.subscriptions.delete(taskId)
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'unsubscribe', task_id: taskId }))
    }
  }
}

export const wsClient = new WSClient()
