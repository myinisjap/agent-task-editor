// Live task list for the Office/Robots scene. Home loads the task list once;
// this keeps it current from WebSocket events (event driven, no polling) and
// tracks rate-limit expiry so a dozing worker wakes when the block clears.
import { useEffect, useRef, useState } from 'react'
import { api, type Task } from '../../api/client'
import { wsClient } from '../../api/ws'
import { useTasksStore } from '../../stores/tasks'
import { useDebouncedCallback } from '../../lib/useDebouncedCallback'

const DEBOUNCE_MS = 250
const COALESCE_MAX = 5

export function useSceneTasks(): { tasks: Task[] | undefined; rateLimitedUntil: Record<string, string>; now: number } {
  const loaded = useTasksStore((s) => s.loaded)
  const all = useTasksStore((s) => s.tasks)
  const [rateLimitedUntil, setRateLimitedUntil] = useState<Record<string, string>>({})
  const [now, setNow] = useState(() => Date.now())
  const pending = useRef<Set<string>>(new Set())

  const flush = useDebouncedCallback(() => {
    const ids = [...pending.current]
    pending.current = new Set()
    if (ids.length === 0) return
    const { upsert, fetch } = useTasksStore.getState()
    if (ids.length <= COALESCE_MAX) {
      for (const id of ids) api.tasks.get(id).then(upsert).catch(() => {})
    } else {
      fetch()
    }
  }, DEBOUNCE_MS)

  useEffect(() => {
    const queue = (id: string) => {
      pending.current.add(id)
      flush()
    }
    const clear = (id: string) =>
      setRateLimitedUntil((prev) => {
        if (!(id in prev)) return prev
        const next = { ...prev }
        delete next[id]
        return next
      })
    return wsClient.on((event) => {
      switch (event.type) {
        case 'task.updated':
        case 'task.created':
          queue(event.payload.id)
          break
        case 'task.label_changed':
          queue(event.payload.task_id)
          clear(event.payload.task_id)
          break
        case 'task.agent_started':
          queue(event.payload.task_id)
          clear(event.payload.task_id)
          break
        case 'task.agent_done':
          queue(event.payload.task_id)
          break
        case 'task.rate_limited':
          setRateLimitedUntil((prev) => ({ ...prev, [event.payload.task_id]: event.payload.unblocked_at }))
          break
        case 'resync':
        case 'task.created_bulk':
          useTasksStore.getState().fetch()
          break
      }
    })
  }, [flush])

  // Wake at the earliest known expiry so derived rate-limit flags recompute.
  const tasks = loaded ? all : undefined
  useEffect(() => {
    let next = Infinity
    const t0 = Date.now()
    const consider = (iso: string | null | undefined) => {
      const ms = iso ? Date.parse(iso) : NaN
      if (Number.isFinite(ms) && ms > t0 && ms < next) next = ms
    }
    for (const v of Object.values(rateLimitedUntil)) consider(v)
    for (const t of tasks ?? []) if (t.block_reason?.code === 'rate_limited') consider(t.block_reason.clears_at)
    if (next === Infinity) return
    const id = setTimeout(() => setNow(Date.now()), Math.min(next - t0 + 50, 2 ** 31 - 1))
    return () => clearTimeout(id)
  }, [tasks, rateLimitedUntil, now])

  return { tasks, rateLimitedUntil, now }
}
