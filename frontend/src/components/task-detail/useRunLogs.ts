import { useCallback, useEffect, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { api, type AgentLog } from '../../api/client'
import { wsClient } from '../../api/ws'
import { LOG_PAGE_SIZE, mergeLogs, toLog } from '../../lib/agentLogMerge'

// useRunLogs owns the log list, pagination, and virtualizer for a single
// (taskId, runId) pair, including live updates via the WS message bus.
//
// IMPORTANT: this hook assumes the parent (TaskDetailPage) has already called
// wsClient.subscribeTask(taskId) — it only registers a wsClient.on() message
// handler (safe to call from multiple components; each just filters events by
// task_id/run_id) and must NOT call subscribeTask/unsubscribeTask itself. Those
// calls key off a Set<string> of task ids (not a refcount), so a second
// subscribe/unsubscribe pair from here — especially one that fires on every
// `runId` change while the parent stays mounted — could unsubscribe the whole
// task's WS stream out from under the parent's own listener.
export function useRunLogs(taskId: string | undefined, runId: string | null, isRunning: boolean) {
  const [debug, setDebug] = useState(false)
  const [logs, setLogs] = useState<AgentLog[]>([])
  const [logsHasEarlier, setLogsHasEarlier] = useState(false)
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  const logScrollRef = useRef<HTMLDivElement>(null)
  const autoScrollRef = useRef(true)
  // When "load earlier" prepends N entries, this holds N so the post-render
  // effect can re-anchor the viewport to the entry that was previously on top
  // (otherwise the virtualized list would jump).
  const anchorIndexRef = useRef<number | null>(null)

  // Virtualize the log list: only entries near the viewport are mounted, so a
  // run with thousands of entries stays smooth. Rows are variable-height
  // (markdown, expandable tool results), so heights are measured dynamically
  // via measureElement rather than estimated up front.
  const logVirtualizer = useVirtualizer({
    count: logs.length,
    getScrollElement: () => logScrollRef.current,
    estimateSize: () => 44,
    overscan: 12,
  })

  // Load the newest page of logs when the selected run changes. Older entries
  // are fetched on demand via "Load earlier".
  useEffect(() => {
    if (!taskId || !runId) return
    let cancelled = false
    api.tasks.runLogs(taskId, runId, { limit: LOG_PAGE_SIZE }).then((res) => {
      if (cancelled) return
      setLogs(res.items)
      setLogsHasEarlier(res.hasMore)
      autoScrollRef.current = true
    }).catch(() => {})
    return () => { cancelled = true }
  }, [taskId, runId])

  // Fetch the page of log entries immediately older than the ones we hold,
  // using the oldest currently-loaded entry's id as the cursor.
  const handleLoadEarlier = useCallback(async () => {
    if (!taskId || !runId || loadingEarlier) return
    const oldest = logs[0]?.id
    if (!oldest) return
    setLoadingEarlier(true)
    try {
      const res = await api.tasks.runLogs(taskId, runId, { before: oldest, limit: LOG_PAGE_SIZE })
      autoScrollRef.current = false
      setLogs((prev) => {
        const merged = mergeLogs(prev, res.items)
        // Number of entries added at the top — used to re-anchor the viewport.
        anchorIndexRef.current = merged.length - prev.length
        return merged
      })
      setLogsHasEarlier(res.hasMore)
    } catch {
      // best-effort; leave the button so the user can retry
    } finally {
      setLoadingEarlier(false)
    }
  }, [taskId, runId, logs, loadingEarlier])

  // WS subscription (message-bus only — see doc comment above).
  useEffect(() => {
    if (!taskId) return
    const off = wsClient.on((event) => {
      if (event.type === 'agent.log' && event.payload.task_id === taskId) {
        const entry = event.payload.entry as AgentLog
        if (entry && event.payload.run_id === runId) {
          const l = toLog(entry)
          setLogs((prev) => (prev.some((x) => x.id === l.id) ? prev : [...prev, l]))
        }
      } else if (event.type === 'agent.log_replay' && event.payload.task_id === taskId) {
        // Batched tail sent on subscribe. Merge (dedupe) with whatever the REST
        // page already loaded, and surface "load earlier" if more history exists.
        if (event.payload.run_id === runId) {
          const entries = (event.payload.entries ?? []).map(toLog)
          setLogs((prev) => mergeLogs(prev, entries))
          if (event.payload.has_more) setLogsHasEarlier(true)
        }
      }
    })
    return () => { off() }
  }, [taskId, runId])

  // Keep the log viewport anchored as entries change. After "load earlier"
  // prepends entries, re-anchor to the entry that was previously on top so the
  // view doesn't jump. Otherwise, when following the tail, scroll to the newest.
  useEffect(() => {
    if (anchorIndexRef.current != null) {
      const idx = anchorIndexRef.current
      anchorIndexRef.current = null
      if (idx > 0) logVirtualizer.scrollToIndex(idx, { align: 'start' })
      return
    }
    if (autoScrollRef.current && logs.length > 0) {
      logVirtualizer.scrollToIndex(logs.length - 1, { align: 'end' })
    }
  }, [logs, logVirtualizer])

  return {
    logs,
    logsHasEarlier,
    loadingEarlier,
    handleLoadEarlier,
    debug,
    setDebug,
    logScrollRef,
    autoScrollRef,
    logVirtualizer,
    isRunning,
  }
}
