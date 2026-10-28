import { Check, CornerDownRight, Hourglass, TriangleAlert } from 'lucide-react'
import Icon from './ui/Icon'
import { labelDisplayNameByName } from '../lib/labelDisplay'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Task, WorkflowLabel } from '../api/client'
import { api } from '../api/client'
import { wsClient } from '../api/ws'

// mergeBadge renders a child's merge-back status.
function mergeBadge(status?: string) {
  if (status === 'merged') return <span className="text-[10px] text-success inline-flex items-center gap-1"><Icon icon={Check} size="xs" />merged</span>
  if (status === 'merge_conflict') return <span className="text-[10px] text-danger inline-flex items-center gap-1"><Icon icon={TriangleAlert} size="xs" />conflict</span>
  if (status === 'pending') return <span className="text-[10px] text-warning inline-flex items-center gap-1"><Icon icon={Hourglass} size="xs" />pending</span>
  return null
}

export type SubtaskStatus = 'running' | 'done' | 'awaiting release' | 'blocked' | 'queued'

// subtaskStatus derives a child's run/blocked status for display.
export function subtaskStatus(child: Task, labels: WorkflowLabel[]): SubtaskStatus {
  const label = labels.find((l) => l.name === child.label)
  if (child.active_run_status) return 'running'
  if (label?.is_terminal) return 'done'
  if (label?.agent_ignore) return 'awaiting release'
  if (child.block_reason?.code === 'dependency') return 'blocked'
  return 'queued'
}

const statusColor: Record<SubtaskStatus, string> = {
  running: 'text-accent-fg',
  done: 'text-success',
  'awaiting release': 'text-fg-muted',
  blocked: 'text-warning',
  queued: 'text-fg-muted',
}

function scopeChips(scope?: string[]) {
  if (!scope || scope.length === 0) return <span className="text-[10px] text-fg-subtle">no scope</span>
  return (
    <span className="flex flex-wrap gap-1">
      {scope.map((g) => (
        <code key={g} className="text-[10px] px-1 rounded bg-surface-raised text-fg-muted">
          {g}
        </code>
      ))}
    </span>
  )
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
      if (e.type === 'resync' || e.type === 'task.updated' || e.type === 'task.label_changed' || e.type === 'task.created' || e.type === 'task.subtask_conflict') {
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
    <div className="border-t border-border pt-3">
      <p className="text-xs text-fg-subtle mb-2">Subtasks</p>

      {isChild && (
        <div className="mb-3 text-xs">
          <button
            onClick={() => navigate(`/tasks/${task.parent_task_id}`)}
            className="inline-flex items-center gap-1 text-accent-fg hover:text-indigo-200"
          >
            <Icon icon={CornerDownRight} size="sm" className="inline -mt-0.5 mr-1" />Part of a parent task — open parent
          </button>
          <div className="mt-1 flex items-center gap-2 text-fg-subtle">
            <span>Merge-back:</span>
            {mergeBadge(task.merge_status) ?? <span className="text-[10px] text-fg-subtle">not yet merged</span>}
          </div>
          <div className="mt-1 flex items-center gap-2 text-fg-subtle">
            <span>Scope:</span>
            {scopeChips(task.file_scope)}
          </div>
        </div>
      )}

      {children.length > 0 && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <p className="text-[11px] uppercase tracking-wide text-fg-subtle">
              Children ({children.filter((c) => mergeIsDone(c)).length}/{children.length})
              {task.integration_status === 'pending' && <span className="ml-2 normal-case text-warning">integration pending</span>}
              {task.integration_status === 'done' && <span className="ml-2 normal-case text-success">integrated</span>}
            </p>
            {gatedChildren.length > 0 && releaseTarget && (
              <button
                onClick={release}
                disabled={busy}
                className="text-[11px] px-2 py-0.5 rounded bg-accent hover:bg-accent/90 text-on-accent disabled:opacity-50"
                title={`Move ${gatedChildren.length} gated subtask(s) into "${releaseTarget}"`}
              >
                Release {gatedChildren.length} → {releaseTarget}
              </button>
            )}
          </div>
          <ul className="flex flex-col gap-1">
            {children.map((c) => (
              <li key={c.id} className="flex flex-col gap-1 text-xs bg-surface-sunken rounded px-2 py-1">
                <div className="flex items-center gap-2">
                <button
                  onClick={() => navigate(`/tasks/${c.id}`)}
                  className="text-fg-muted hover:text-accent-fg truncate text-left flex-1"
                  title={c.title}
                >
                  {c.title}
                </button>
                <span className="text-fg-subtle shrink-0">{labelDisplayNameByName(c.label, labels)}</span>
                <span data-testid="subtask-status" className={`shrink-0 text-[10px] ${statusColor[subtaskStatus(c, labels)]}`}>
                  {subtaskStatus(c, labels)}
                </span>
                {mergeBadge(c.merge_status)}
                </div>
                {scopeChips(c.file_scope)}
              </li>
            ))}
          </ul>
          {error && <p className="text-xs text-danger mt-1">{error}</p>}
        </div>
      )}
    </div>
  )
}

function mergeIsDone(c: Task): boolean {
  return c.merge_status === 'merged'
}
