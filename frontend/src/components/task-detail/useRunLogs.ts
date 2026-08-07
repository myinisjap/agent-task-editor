import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { api, type AgentLog } from '../../api/client'
import { wsClient } from '../../api/ws'
import { LOG_PAGE_SIZE, mergeLogs, toLog } from '../../lib/agentLogMerge'
import { groupLogRows } from '../../lib/groupAgentLog'

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
  // After "load earlier" prepends entries, this holds the row that was on top
  // so the post-render effect can re-anchor the viewport to it (otherwise the
  // virtualized list would jump). The row count is a fallback for when that row
  // no longer exists on its own — an orphan result folds into its call once the
  // older page brings the call into view.
  const anchorRef = useRef<{ key: string; rowCount: number } | null>(null)

  // Display rows: a tool result is folded into the call that produced it, so a
  // call and its output are one row rather than two. See groupAgentLog.
  const rows = useMemo(() => groupLogRows(logs, debug, isRunning), [logs, debug, isRunning])

  // Virtualize the log list: only entries near the viewport are mounted, so a
  // run with thousands of entries stays smooth. Rows are variable-height
  // (markdown, expandable tool results), so heights are measured dynamically
  // via measureElement rather than estimated up front.
  const logVirtualizer = useVirtualizer({
    count: rows.length,
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
      anchorRef.current = rows[0] ? { key: rows[0].key, rowCount: rows.length } : null
      setLogs((prev) => mergeLogs(prev, res.items))
      setLogsHasEarlier(res.hasMore)
    } catch {
      // best-effort; leave the button so the user can retry
    } finally {
      setLoadingEarlier(false)
    }
  }, [taskId, runId, logs, rows, loadingEarlier])

  // WS subscription (message-bus only — see doc comment above).
  useEffect(() => {
    if (!taskId) return
    const off = wsClient.on((event) => {
      if (event.type === 'agent.log' && event.payload.task_id === taskId) {
        const entry = event.payload.entry as AgentLog
        if (entry && event.payload.run_id === runId) {
          // entry.id is the persisted agent_logs row id (see toLog's doc
          // comment), matching what agent.log_replay/REST pages return for
          // the same row, so this guard actually catches reconnect
          // duplicates rather than always seeing a fresh client-minted id.
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
    const anchor = anchorRef.current
    if (anchor) {
      anchorRef.current = null
      const found = rows.findIndex((r) => r.key === anchor.key)
      const idx = found >= 0 ? found : rows.length - anchor.rowCount
      if (idx > 0) logVirtualizer.scrollToIndex(idx, { align: 'start' })
      return
    }
    if (autoScrollRef.current && rows.length > 0) {
      logVirtualizer.scrollToIndex(rows.length - 1, { align: 'end' })
    }
  }, [rows, logVirtualizer])

  return {
    logs,
    rows,
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
