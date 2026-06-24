import type { Task, AgentLog } from './client'

export type WSEvent =
  | { type: 'task.label_changed'; payload: { task_id: string; from: string; to: string; note?: string } }
  | { type: 'task.agent_started'; payload: { task_id: string; run_id: string; agent_name: string } }
  | { type: 'task.agent_done'; payload: { task_id: string; run_id: string; status: string } }
  | { type: 'task.needs_human'; payload: { task_id: string; run_id: string; message: string } }
  | { type: 'agent.log'; payload: { task_id: string; run_id: string; entry: AgentLog } }
  | { type: 'task.created'; payload: Task }
  | { type: 'task.updated'; payload: Task }

type Handler = (event: WSEvent) => void

class WSClient {
  private ws: WebSocket | null = null
  private handlers: Handler[] = []
  private subscriptions = new Set<string>()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null

  connect() {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    this.ws = new WebSocket(`${proto}//${window.location.host}/ws`)

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
      this.ws.send(JSON.stringify({ type: 'subscribe_task', task_id: taskId }))
    }
  }

  unsubscribeTask(taskId: string) {
    this.subscriptions.delete(taskId)
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'unsubscribe_task', task_id: taskId }))
    }
  }
}

export const wsClient = new WSClient()
