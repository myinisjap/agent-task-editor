import { Fragment, useState } from 'react'
import type { AgentRun, AgentConfig } from '../../api/client'

// formatTokenCount renders a token count compactly (e.g. "1.2k") for the
// per-run cost/usage badge, falling back to the plain number for small counts.
function formatTokenCount(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

function NotesPanel({ notes }: { notes: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-slate-700 pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300 w-full text-left py-0.5"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span>agent notes</span>
      </button>
      {open && (
        <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {notes}
        </pre>
      )}
    </div>
  )
}

function StoredInfoPanel({ runId, info }: { runId: string; info: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-slate-700 pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300 w-full text-left py-0.5"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span className="font-mono text-slate-600">{runId.slice(0, 8)}</span>
        <span>stored info</span>
      </button>
      {open && (
        <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {info}
        </pre>
      )}
    </div>
  )
}

export default function RunHistoryList({
  runs,
  agentConfigs,
  selectedRun,
  onSelectRun,
  isRunning,
  canRerun,
  onStop,
  onRerun,
  actionPending,
}: {
  runs: AgentRun[]
  agentConfigs: AgentConfig[]
  selectedRun: string | null
  onSelectRun: (runId: string) => void
  isRunning: boolean
  canRerun: boolean
  onStop: () => void
  onRerun: () => void
  actionPending: boolean
}) {
  if (runs.length === 0 && !isRunning && !canRerun) return null

  return (
    <>
      {runs.length > 0 && (
        <div>
          <p className="text-xs text-slate-500 mb-2">Agent runs</p>
          <div className="flex flex-col gap-1">
            {runs.map((run) => (
              <Fragment key={run.id}>
                <button
                  onClick={() => onSelectRun(run.id)}
                  className={`text-left text-xs px-2 py-1.5 rounded ${
                    selectedRun === run.id
                      ? 'bg-slate-700 text-slate-100'
                      : 'text-slate-400 hover:bg-slate-800'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono truncate">{run.id.slice(0, 8)}</span>
                    {agentConfigs.find((a) => a.id === run.agent_config_id)?.name && (
                      <span className="text-slate-400 truncate text-xs">
                        {agentConfigs.find((a) => a.id === run.agent_config_id)?.name}
                      </span>
                    )}
                    <span className={`shrink-0 ${
                      run.status === 'completed'     ? 'text-emerald-400' :
                      run.status === 'running'       ? 'text-yellow-400 animate-pulse' :
                      run.status === 'failed'        ? 'text-red-400' :
                      run.status === 'waiting_human' ? 'text-pink-400' :
                      run.status === 'cancelled'     ? 'text-orange-400' :
                      'text-slate-500'
                    }`}>{run.status}</span>
                  </div>
                  {((run.cost_usd ?? 0) > 0 || (run.input_tokens ?? 0) > 0 || (run.output_tokens ?? 0) > 0) && (
                    <div className="text-slate-500 text-[11px] mt-0.5">
                      ${(run.cost_usd ?? 0).toFixed(4)} · {formatTokenCount(run.input_tokens ?? 0)}/{formatTokenCount(run.output_tokens ?? 0)} tok
                    </div>
                  )}
                </button>
                {run.stored_info && (
                  <StoredInfoPanel runId={run.id} info={run.stored_info} />
                )}
                {run.notes && (
                  <NotesPanel notes={run.notes} />
                )}
              </Fragment>
            ))}
          </div>
        </div>
      )}

      {isRunning && (
        <button
          onClick={onStop}
          disabled={actionPending}
          className="w-full px-3 py-1.5 text-xs font-medium rounded bg-red-900/60 hover:bg-red-800 text-red-200 disabled:opacity-50"
          title="Stop the running agent and pause the task"
        >
          ■ Stop run
        </button>
      )}

      {canRerun && (
        <button
          onClick={onRerun}
          disabled={actionPending}
          className="w-full px-3 py-1.5 text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200 disabled:opacity-50"
        >
          ↻ Re-run
        </button>
      )}
    </>
  )
}
