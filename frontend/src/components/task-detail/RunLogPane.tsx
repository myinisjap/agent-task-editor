import { useEffect, useMemo, useState } from 'react'
import type { AgentConfig, AgentRun, ProviderConfig, TaskLabelHistoryEntry } from '../../api/client'
import AgentLogEntry from '../board/AgentLogEntry'
import type { LogRow } from '../../lib/groupAgentLog'
import { commandOutcome, deriveRunSummary, type StepGroup } from '../../lib/runTimeline'
import NextStepLine from './NextStepLine'
import RunStepGroup from './RunStepGroup'
import RunSummaryCard from './RunSummaryCard'
import { useRunLogs } from './useRunLogs'

// RunLogPane renders the Logs tab: entry list (virtualized), "load earlier",
// and the debug toggle. See useRunLogs for the WS-subscription assumption
// (the parent must already have called wsClient.subscribeTask(taskId)).
export default function RunLogPane({
  taskId, runId, isRunning, run, agentConfigs, providerConfigs, labelHistory, onRetry, onReply, onOpenDiff,
}: {
  taskId: string | undefined
  runId: string | null
  isRunning: boolean
  /** The selected run; enables the summary card and the closing next-step line. */
  run?: AgentRun
  agentConfigs?: AgentConfig[]
  providerConfigs?: ProviderConfig[]
  labelHistory?: TaskLabelHistoryEntry[]
  onRetry?: () => void
  onReply?: () => void
  onOpenDiff?: () => void
}) {
  const {
    logs,
    baseRows,
    items,
    now,
    logsHasEarlier,
    loadingEarlier,
    handleLoadEarlier,
    debug,
    setDebug,
    logScrollRef,
    autoScrollRef,
    logVirtualizer,
  } = useRunLogs(taskId, runId, isRunning)

  const summary = useMemo(
    () => (run ? deriveRunSummary({ run, rows: baseRows, agentConfigs, providerConfigs, labelHistory, now }) : null),
    [run, baseRows, agentConfigs, providerConfigs, labelHistory, now],
  )

  // Explicit user open/close choices, keyed by group key. Held here (not in the
  // group) because virtualization unmounts off-screen groups.
  const [overrides, setOverrides] = useState<Map<string, boolean>>(() => new Map())
  useEffect(() => { setOverrides(new Map()) }, [runId])
  const lastKey = items.length > 0 ? items[items.length - 1].key : null
  const isOpen = (g: StepGroup) =>
    overrides.get(g.key) ?? (g.hasError || (isRunning && g.key === lastKey))
  const toggle = (g: StepGroup) =>
    setOverrides((prev) => new Map(prev).set(g.key, !isOpen(g)))

  return (
    <div className="h-full flex flex-col" data-testid="run-log-pane">
      <p className="text-slate-500 text-xs py-3 px-3 font-sans flex items-center gap-2 shrink-0">
        {isRunning && <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />}
        {runId ? `Run ${runId.slice(0, 8)}` : 'No agent runs yet'}
        {logs.length > 0 && <span className="text-slate-600">· {logs.length} events</span>}
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
      {summary && <RunSummaryCard summary={summary} partial={logsHasEarlier} />}
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
          {logVirtualizer.getVirtualItems().map((vi) => {
            const item = items[vi.index]
            if (!item) return null
            return (
            <div
              key={item.key ?? vi.index}
              data-index={vi.index}
              ref={logVirtualizer.measureElement}
              style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vi.start}px)` }}
            >
              {debug ? (
                <AgentLogEntry row={item as LogRow} />
              ) : (
                <RunStepGroup
                  group={item as StepGroup}
                  open={isOpen(item as StepGroup)}
                  onToggle={() => toggle(item as StepGroup)}
                  failed={(item as StepGroup).rows.some((r) => commandOutcome(r).state === 'failed')}
                />
              )}
            </div>
            )
          })}
        </div>
      </div>
      {summary?.nextStep && !isRunning && (
        <div className="px-2 pb-2 shrink-0">
          <NextStepLine next={summary.nextStep} onReply={onReply} onRetry={onRetry} onOpenDiff={onOpenDiff} />
        </div>
      )}
    </div>
  )
}
