import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Task, WorkflowLabel } from '../api/client'
import { api } from '../api/client'
import { wsClient } from '../api/ws'

// mergeBadge renders a child's merge-back status.
function mergeBadge(status?: string) {
  if (status === 'merged') return <span className="text-[10px] text-emerald-400">✓ merged</span>
  if (status === 'merge_conflict') return <span className="text-[10px] text-red-400">⚠ conflict</span>
  if (status === 'pending') return <span className="text-[10px] text-amber-400">⏳ pending</span>
  return null
}

// SubtasksPanel shows a task's subtask relationships (Mechanism 2): for a child,
// a link to its parent plus its merge-back status; for a parent, the list of
// children with a bulk "release" action to move gated children into the flow.
export default function SubtasksPanel({
  task,
  labels,
  onChanged,
}: {
  task: Task
  labels: WorkflowLabel[]
  onChanged?: () => void
}) {
  const navigate = useNavigate()
  const [children, setChildren] = useState<Task[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api.tasks.subtasks(task.id).then(setChildren).catch(() => setChildren([]))
  }, [task.id])

  useEffect(load, [load])
  useEffect(() => {
    return wsClient.on((e) => {
      if (e.type === 'task.updated' || e.type === 'task.label_changed' || e.type === 'task.created' || e.type === 'task.subtask_conflict') {
        load()
      }
    })
  }, [load])

  const isChild = !!task.parent_task_id

  // Gate labels are agent_ignore; the release target is the first non-gate label.
  const sorted = [...labels].sort((a, b) => a.sort_order - b.sort_order)
  const gateNames = new Set(sorted.filter((l) => l.agent_ignore).map((l) => l.name))
  const releaseTarget = sorted.find((l) => !l.agent_ignore)?.name
  const gatedChildren = children.filter((c) => gateNames.has(c.label))

  const release = async () => {
    if (!releaseTarget || gatedChildren.length === 0) return
    setBusy(true)
    setError('')
    try {
      await api.tasks.bulk(gatedChildren.map((c) => c.id), 'move', { to_label: releaseTarget })
      load()
      onChanged?.()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  if (!isChild && children.length === 0) return null

  return (
    <div className="border-t border-slate-800 pt-3">
      <p className="text-xs text-slate-500 mb-2">Subtasks</p>

      {isChild && (
        <div className="mb-3 text-xs">
          <button
            onClick={() => navigate(`/tasks/${task.parent_task_id}`)}
            className="inline-flex items-center gap-1 text-indigo-300 hover:text-indigo-200"
          >
            ↳ Part of a parent task — open parent
          </button>
          <div className="mt-1 flex items-center gap-2 text-slate-500">
            <span>Merge-back:</span>
            {mergeBadge(task.merge_status) ?? <span className="text-[10px] text-slate-500">not yet merged</span>}
          </div>
        </div>
      )}

      {children.length > 0 && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <p className="text-[11px] uppercase tracking-wide text-slate-600">
              Children ({children.filter((c) => mergeIsDone(c)).length}/{children.length})
            </p>
            {gatedChildren.length > 0 && releaseTarget && (
              <button
                onClick={release}
                disabled={busy}
                className="text-[11px] px-2 py-0.5 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
                title={`Move ${gatedChildren.length} gated subtask(s) into "${releaseTarget}"`}
              >
                Release {gatedChildren.length} → {releaseTarget}
              </button>
            )}
          </div>
          <ul className="flex flex-col gap-1">
            {children.map((c) => (
              <li key={c.id} className="flex items-center gap-2 text-xs bg-slate-800 rounded px-2 py-1">
                <button
                  onClick={() => navigate(`/tasks/${c.id}`)}
                  className="text-slate-300 hover:text-indigo-300 truncate text-left flex-1"
                  title={c.title}
                >
                  {c.title}
                </button>
                <span className="text-slate-500 shrink-0">{c.label}</span>
                {mergeBadge(c.merge_status)}
              </li>
            ))}
          </ul>
          {error && <p className="text-xs text-red-400 mt-1">{error}</p>}
        </div>
      )}
    </div>
  )
}

function mergeIsDone(c: Task): boolean {
  return c.merge_status === 'merged'
}
