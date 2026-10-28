import { useEffect, useRef, useState } from 'react'
import { wsClient } from '../api/ws'
import { parseLogContent } from './parseAgentLog'

export type RunStep = { tool: string; summary: string }

/** Formats a tool_call log entry; null for anything else. */
export function stepFromEntry(entry: { type: string; content: string }): RunStep | null {
  if (entry.type !== 'tool_call') return null
  const parsed = parseLogContent('tool_call', entry.content)
  if (parsed.kind !== 'tool_call') return null
  return { tool: parsed.toolName, summary: parsed.summary }
}

/**
 * Latest tool call per active run, keyed by run_id. Subscribes to each run's task.
 * NOTE: wsClient subscriptions are a Set, not a refcount, so unsubscribing here would
 * drop a subscription TaskDetailPage holds. Home and TaskDetail are separate routes and
 * never mounted together today; revisit if the Inbox popup shows task detail over Home.
 */
export function useRunSteps(runs: { run_id: string; task_id: string }[]): Record<string, RunStep> {
  const [steps, setSteps] = useState<Record<string, RunStep>>({})
  const runIds = useRef<Set<string>>(new Set())
  const subscribed = useRef<Set<string>>(new Set())
  runIds.current = new Set(runs.map((r) => r.run_id))
  const key = [...new Set(runs.map((r) => r.task_id))].sort().join(',')
  const runKey = [...runIds.current].sort().join(',')

  useEffect(() => {
    const off = wsClient.on((ev) => {
      if (ev.type === 'agent.log') {
        const { run_id, entry } = ev.payload
        if (!runIds.current.has(run_id)) return
        const s = stepFromEntry(entry)
        if (s) setSteps((p) => ({ ...p, [run_id]: s }))
      } else if (ev.type === 'agent.log_replay') {
        const { run_id, entries } = ev.payload
        if (!runIds.current.has(run_id)) return
        for (let i = entries.length - 1; i >= 0; i--) {
          const s = stepFromEntry(entries[i])
          if (s) {
            setSteps((p) => ({ ...p, [run_id]: s }))
            break
          }
        }
      }
    })
    const subs = subscribed.current
    return () => {
      off()
      subs.forEach((id) => wsClient.unsubscribeTask(id))
      subs.clear()
    }
  }, [])

  useEffect(() => {
    const want = new Set(key ? key.split(',') : [])
    const subs = subscribed.current
    for (const id of want) if (!subs.has(id)) { subs.add(id); wsClient.subscribeTask(id) }
    for (const id of [...subs]) if (!want.has(id)) { subs.delete(id); wsClient.unsubscribeTask(id) }
  }, [key])

  useEffect(() => {
    setSteps((p) => {
      const keep = Object.keys(p).filter((r) => runIds.current.has(r))
      if (keep.length === Object.keys(p).length) return p
      return Object.fromEntries(keep.map((r) => [r, p[r]]))
    })
  }, [runKey])

  return steps
}
