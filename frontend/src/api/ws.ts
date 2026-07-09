import type { Task, AgentLog } from './client'

export type WSEvent =
  | { type: 'task.label_changed'; payload: { task_id: string; from: string; to: string; note?: string } }
  | { type: 'task.agent_started'; payload: { task_id: string; run_id: string; agent_name: string } }
  | { type: 'task.agent_done'; payload: { task_id: string; run_id: string; status: string } }
  | { type: 'task.needs_human'; payload: { task_id: string; run_id: string; message: string } }
  | { type: 'task.rate_limited'; payload: { task_id: string; run_id: string; agent_config_id: string; unblocked_at: string } }
  | { type: 'agent.log'; payload: { task_id: string; run_id: string; entry: AgentLog } }
  // Sent once on subscribe: the tail of the run's persisted log as a single
  // batched message (capped server-side). has_more signals that earlier entries
  // exist and can be fetched via the REST logs endpoint ("load earlier").
  | { type: 'agent.log_replay'; payload: { task_id: string; run_id: string; has_more: boolean; entries: AgentLog[] } }
  | { type: 'task.git_state_changed'; payload: { task_id: string; git_state: string; pr_url: string } }
  | { type: 'task.review_comments_changed'; payload: { task_id: string; run_id: string; resolved: number } }
  // task.created payloads carry a subset of Task fields (always includes id);
  // consumers should refetch the task for full data.
  | { type: 'task.created'; payload: Pick<Task, 'id' | 'title' | 'label' | 'repo_id' | 'source' | 'source_ref'> }
  | { type: 'task.updated'; payload: Task }
  | { type: 'task.subtask_conflict'; payload: { task_id: string; parent_id: string; files: string[] } }

type Handler = (event: WSEvent) => void

class WSClient {
  private ws: WebSocket | null = null
  private handlers: Handler[] = []
  private subscriptions = new Set<string>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null

  async connect() {
    // Already connected or connecting — don't double-connect
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const token = import.meta.env.VITE_API_TOKEN
    const base = import.meta.env.BASE_URL.replace(/\/$/, '')

    // If a token is configured, exchange it for a short-lived, single-use
    // ticket via the bearer-authed REST endpoint rather than putting the
    // long-lived token itself in the WS URL (query strings leak into
    // reverse-proxy/access logs and browser history).
    let ticketParam = ''
    if (token) {
      try {
        const res = await fetch(`${base}/api/v1/ws-ticket`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${token}` },
        })
        if (res.ok) {
          const { ticket } = await res.json()
          if (ticket) ticketParam = `?ticket=${encodeURIComponent(ticket)}`
        }
      } catch {
        // Fall through with no ticket — the connection will 401 and the
        // existing onclose reconnect loop will retry.
      }
    }

    const url = `${proto}//${window.location.host}${base}/ws${ticketParam}`
    this.ws = new WebSocket(url)

    this.ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as WSEvent
        this.handlers.forEach((h) => h(event))
      } catch {
        // ignore malformed messages
      }
    }

    this.ws.onclose = () => {
      this.reconnectTimer = setTimeout(() => this.connect(), 3000)
    }

    this.ws.onopen = () => {
      if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
      // Re-subscribe on reconnect
      this.subscriptions.forEach((id) => this.subscribeTask(id))
    }
  }

  on(handler: Handler) {
    this.handlers.push(handler)
    return () => {
      this.handlers = this.handlers.filter((h) => h !== handler)
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
