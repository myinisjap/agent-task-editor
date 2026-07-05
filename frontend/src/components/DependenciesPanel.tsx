import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Task, TaskDependencies } from '../api/client'
import { api } from '../api/client'
import { wsClient } from '../api/ws'

// DependenciesPanel renders a task's peer dependencies: the tasks it is blocked
// by (with live met/unmet state and a same-workflow picker to add more) and the
// tasks that depend on it. Edges are a pure dispatch gate — see Mechanism 1.
export default function DependenciesPanel({
  task,
  onChanged,
}: {
  task: Task
  // Called after an edge is added/removed so the parent can refresh the task's
  // derived counts / blocked styling.
  onChanged?: () => void
}) {
  const navigate = useNavigate()
  const [deps, setDeps] = useState<TaskDependencies | null>(null)
  const [candidates, setCandidates] = useState<Task[]>([])
  const [selected, setSelected] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api.tasks
      .dependencies(task.id)
      .then(setDeps)
      .catch(() => setDeps(null))
  }, [task.id])

  useEffect(load, [load])

  // Keep met/unmet state live: a blocker finishing elsewhere sends task.updated
  // (the engine nudges dependents), so refetch on any task event.
  useEffect(() => {
    return wsClient.on((e) => {
      if (
        e.type === 'task.updated' ||
        e.type === 'task.label_changed' ||
        e.type === 'task.agent_done'
      ) {
        load()
      }
    })
  }, [load])

  // Candidate blockers: same-workflow tasks that aren't this task or already a
  // blocker. Loaded lazily; the list API has no workflow filter so we filter here.
  const loadCandidates = useCallback(() => {
    api.tasks
      .list({}, { limit: 500 })
      .then((page) => setCandidates(page.items))
      .catch(() => setCandidates([]))
  }, [])

  const blockerIds = useMemo(
    () => new Set((deps?.blocked_by ?? []).map((b) => b.task_id)),
    [deps],
  )
  const options = candidates.filter(
    (c) => c.workflow_id === task.workflow_id && c.id !== task.id && !blockerIds.has(c.id),
  )

  const add = async () => {
    if (!selected) return
    setBusy(true)
    setError('')
    try {
      await api.tasks.addDependency(task.id, selected)
      setSelected('')
      load()
      onChanged?.()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (depId: string) => {
    setBusy(true)
    setError('')
    try {
      await api.tasks.removeDependency(task.id, depId)
      load()
      onChanged?.()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const blockedBy = deps?.blocked_by ?? []
  const blocking = deps?.blocking ?? []

  return (
    <div className="border-t border-slate-800 pt-3">
      <p className="text-xs text-slate-500 mb-2">Dependencies</p>

      {/* Blockers */}
      <div className="mb-3">
        <p className="text-[11px] uppercase tracking-wide text-slate-600 mb-1">Blocked by</p>
        {blockedBy.length === 0 ? (
          <p className="text-xs text-slate-600">No blockers — nothing is gating this task.</p>
        ) : (
          <ul className="flex flex-col gap-1">
            {blockedBy.map((b) => (
              <li
                key={b.task_id}
                className="flex items-center gap-2 text-xs bg-slate-800 rounded px-2 py-1"
              >
                <span
                  title={b.satisfied ? 'Satisfied' : 'Waiting — blocker not yet terminal'}
                  className={b.satisfied ? 'text-emerald-400' : 'text-amber-400'}
                >
                  {b.satisfied ? '✓' : '○'}
                </span>
                <button
                  onClick={() => navigate(`/tasks/${b.task_id}`)}
                  className="text-slate-300 hover:text-indigo-300 truncate text-left flex-1"
                  title={b.title}
                >
                  {b.title}
                </button>
                <span className="text-slate-500 shrink-0">{b.label}</span>
                <button
                  onClick={() => remove(b.task_id)}
                  disabled={busy}
                  className="text-slate-600 hover:text-red-400 disabled:opacity-50 shrink-0"
                  title="Remove dependency"
                >
                  ✕
                </button>
              </li>
            ))}
          </ul>
        )}

        {/* Add-blocker picker */}
        <div className="flex items-center gap-2 mt-2">
          <select
            value={selected}
            onFocus={loadCandidates}
            onClick={loadCandidates}
            onChange={(e) => setSelected(e.target.value)}
            className="flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-2 py-1 text-slate-200 focus:outline-none focus:border-indigo-400"
          >
            <option value="">Add a blocker…</option>
            {options.map((o) => (
              <option key={o.id} value={o.id}>
                {o.title} ({o.label})
              </option>
            ))}
          </select>
          <button
            onClick={add}
            disabled={busy || !selected}
            className="text-xs px-2 py-1 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
          >
            Add
          </button>
        </div>
        {error && <p className="text-xs text-red-400 mt-1">{error}</p>}
      </div>

      {/* Dependents */}
      {blocking.length > 0 && (
        <div>
          <p className="text-[11px] uppercase tracking-wide text-slate-600 mb-1">Blocking</p>
          <ul className="flex flex-col gap-1">
            {blocking.map((d) => (
              <li
                key={d.task_id}
                className="flex items-center gap-2 text-xs bg-slate-800 rounded px-2 py-1"
              >
                <button
                  onClick={() => navigate(`/tasks/${d.task_id}`)}
                  className="text-slate-300 hover:text-indigo-300 truncate text-left flex-1"
                  title={d.title}
                >
                  {d.title}
                </button>
                <span className="text-slate-500 shrink-0">{d.label}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
