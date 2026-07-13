import AgentLogEntry from '../board/AgentLogEntry'
import { useRunLogs } from './useRunLogs'

// RunLogPane renders the Logs tab: entry list (virtualized), "load earlier",
// and the debug toggle. See useRunLogs for the WS-subscription assumption
// (the parent must already have called wsClient.subscribeTask(taskId)).
export default function RunLogPane({ taskId, runId, isRunning }: {
  taskId: string | undefined
  runId: string | null
  isRunning: boolean
}) {
  const {
    logs,
    logsHasEarlier,
    loadingEarlier,
    handleLoadEarlier,
    debug,
    setDebug,
    logScrollRef,
    autoScrollRef,
    logVirtualizer,
  } = useRunLogs(taskId, runId, isRunning)

  return (
    <div className="h-full flex flex-col" data-testid="run-log-pane">
      <p className="text-slate-500 text-xs py-3 px-3 font-sans flex items-center gap-2 shrink-0">
        {isRunning && <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />}
        {runId ? `Run ${runId.slice(0, 8)}` : 'No agent runs yet'}
        {logs.length > 0 && <span className="text-slate-700">· {logs.length} events</span>}
        <label className="ml-2 flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            className="rounded border-slate-700 bg-slate-800 text-indigo-600 focus:ring-indigo-500"
            checked={debug}
            onChange={(e) => setDebug(e.target.checked)}
          />
          <span className="text-slate-400">Debug</span>
        </label>
      </p>
      {logs.length === 0 && runId && (
        <p className="text-slate-600 text-xs px-3">No log entries</p>
      )}
      {logsHasEarlier && (
        <div className="flex justify-center pb-2 shrink-0">
          <button
            onClick={handleLoadEarlier}
            disabled={loadingEarlier}
            className="text-xs px-3 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 disabled:opacity-50"
          >
            {loadingEarlier ? 'Loading…' : '↑ Load earlier'}
          </button>
        </div>
      )}
      {/* Virtualized log list — only rows near the viewport are mounted. */}
      <div
        ref={logScrollRef}
        className="flex-1 overflow-y-auto px-2"
        onScroll={(e) => {
          const el = e.currentTarget
          autoScrollRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
      >
        <div style={{ height: logVirtualizer.getTotalSize(), position: 'relative', width: '100%' }}>
          {logVirtualizer.getVirtualItems().map((vi) => (
            <div
              key={logs[vi.index].id ?? vi.index}
              data-index={vi.index}
              ref={logVirtualizer.measureElement}
              style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vi.start}px)` }}
            >
              <AgentLogEntry log={logs[vi.index]} debug={debug} />
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
