import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Task, type AgentRun, type AgentLog } from '../api/client'

const LOG_COLORS: Record<string, string> = {
  stdout:      'text-slate-300',
  stderr:      'text-red-400',
  system:      'text-yellow-400',
  tool_call:   'text-cyan-400',
  tool_result: 'text-emerald-400',
}

const LABEL_COLORS: Record<string, string> = {
  'not_ready':    '#6B7280',
  'plan':         '#8B5CF6',
  'todo':         '#3B82F6',
  'in-progress':  '#F59E0B',
  'testing':      '#F97316',
  'agent-review': '#6366F1',
  'review':       '#EC4899',
  'done':         '#10B981',
}

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<AgentRun[]>([])
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [logs, setLogs] = useState<AgentLog[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!id) return
    Promise.all([api.tasks.get(id), api.tasks.runs(id)])
      .then(([t, r]) => {
        setTask(t)
        setRuns(r ?? [])
        if (r && r.length > 0) setSelectedRun(r[0].id)
      })
      .finally(() => setLoading(false))
  }, [id])

  useEffect(() => {
    if (!id || !selectedRun) return
    api.tasks.runLogs(id, selectedRun).then((l) => setLogs(l ?? []))
  }, [id, selectedRun])

  if (loading) return <div className="p-6 text-slate-400">Loading…</div>
  if (!task) return <div className="p-6 text-slate-400">Task not found</div>

  const labelColor = LABEL_COLORS[task.label] ?? '#6B7280'

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left panel — metadata */}
      <div className="w-72 shrink-0 border-r border-slate-800 overflow-y-auto p-5 flex flex-col gap-4">
        <button
          onClick={() => navigate('/board')}
          className="text-xs text-slate-500 hover:text-slate-300 text-left"
        >
          ← Board
        </button>
        <div>
          <h1 className="text-lg font-semibold text-slate-100 leading-snug">{task.title}</h1>
          {task.description && (
            <p className="text-sm text-slate-400 mt-2">{task.description}</p>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <span className="text-xs text-slate-500 w-16">Label</span>
            <span
              className="text-xs px-2 py-0.5 rounded-full font-medium text-white"
              style={{ backgroundColor: labelColor }}
            >
              {task.label}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-slate-500 w-16">Type</span>
            <span className="text-xs text-slate-300">{task.type}</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-slate-500 w-16">Created</span>
            <span className="text-xs text-slate-400">
              {new Date(task.created_at).toLocaleDateString()}
            </span>
          </div>
        </div>

        {runs.length > 0 && (
          <div>
            <p className="text-xs text-slate-500 mb-2">Agent runs</p>
            <div className="flex flex-col gap-1">
              {runs.map((run) => (
                <button
                  key={run.id}
                  onClick={() => setSelectedRun(run.id)}
                  className={`text-left text-xs px-2 py-1.5 rounded ${
                    selectedRun === run.id
                      ? 'bg-slate-700 text-slate-100'
                      : 'text-slate-400 hover:bg-slate-800'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <span className="font-mono">{run.id.slice(0, 8)}</span>
                    <span className={`text-xs ${
                      run.status === 'completed' ? 'text-emerald-400' :
                      run.status === 'running'   ? 'text-yellow-400 animate-pulse' :
                      run.status === 'failed'    ? 'text-red-400' :
                      'text-slate-500'
                    }`}>{run.status}</span>
                  </div>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Center panel — agent log stream */}
      <div className="flex-1 overflow-y-auto p-5 font-mono text-xs">
        <p className="text-slate-500 mb-3">
          {selectedRun ? `Run ${selectedRun.slice(0, 8)}` : 'No agent runs yet'}
        </p>
        {logs.length === 0 && selectedRun && (
          <p className="text-slate-600">No log entries</p>
        )}
        {logs.map((log) => (
          <div key={log.id} className={`mb-0.5 ${LOG_COLORS[log.type] ?? 'text-slate-400'}`}>
            <span className="text-slate-600 mr-2 select-none">
              [{log.type.padEnd(11)}]
            </span>
            {log.content}
          </div>
        ))}
      </div>

      {/* Right panel — placeholder for diff viewer (Phase 8) */}
      <div className="w-80 shrink-0 border-l border-slate-800 overflow-y-auto p-5">
        <p className="text-xs text-slate-500 mb-2">File changes</p>
        <p className="text-xs text-slate-600">Git diff viewer — Phase 8</p>
      </div>
    </div>
  )
}
