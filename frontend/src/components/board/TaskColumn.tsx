import { useCallback, useState } from 'react'
import { useDroppable } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import TaskCard from './TaskCard'

const MAX_VISIBLE = 5

type Props = {
  label: WorkflowLabel
  tasks: Task[]
  runningTaskIds: Set<string>
  rateLimitedTaskIds?: Map<string, string>
  costWarnedTaskIds?: Set<string>
  onAddTask?: () => void
  onDuplicate?: (task: Task) => void
  isStartingColumn?: boolean
  isTerminal?: boolean
  className?: string
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
}

export default function TaskColumn({ label, tasks, runningTaskIds, rateLimitedTaskIds, costWarnedTaskIds, onAddTask, onDuplicate, isStartingColumn, isTerminal, className, selectedIds, onToggleSelect }: Props) {
  const { setNodeRef, isOver } = useDroppable({ id: label.name })
  const remove = useTasksStore((s) => s.remove)
  const [expanded, setExpanded] = useState(false)

  const shouldCollapse = !!isTerminal && tasks.length > MAX_VISIBLE
  const visibleTasks = shouldCollapse && !expanded ? tasks.slice(0, MAX_VISIBLE) : tasks

  const wipLimit = label.wip_limit != null && label.wip_limit > 0 ? label.wip_limit : null
  // >= matches the dispatcher's enforcement point: a hard-limited column is
  // already actively blocking new dispatch once it *reaches* its limit, not
  // only once it exceeds it (see checkWIPLimit in backend/internal/agent/dispatcher.go).
  const isAtOrOverLimit = wipLimit != null && tasks.length >= wipLimit
  const isHardAtOrOverLimit = isAtOrOverLimit && label.wip_limit_hard !== 0
  const badgeClassName = isAtOrOverLimit
    ? isHardAtOrOverLimit
      ? 'text-xs text-red-300 bg-red-900/60 border border-red-700 rounded-full px-2 py-0.5'
      : 'text-xs text-amber-300 bg-amber-900/40 border border-amber-700 rounded-full px-2 py-0.5'
    : 'text-xs text-slate-500 bg-slate-800 rounded-full px-2 py-0.5'
  const badgeTitle = isHardAtOrOverLimit
    ? `At/over WIP limit (${tasks.length}/${wipLimit}) — dispatcher will hold new tasks back from this label until it drops below the limit`
    : isAtOrOverLimit
      ? `At/over WIP limit (${tasks.length}/${wipLimit}) — visual warning only, dispatch is not blocked`
      : undefined

  // Stable callback identity (keyed only on `remove`, not per-render) so
  // memo(TaskCard) below can actually skip re-rendering unrelated cards.
  // TaskCard calls onDelete(task.id) directly rather than the column handing
  // it a pre-bound per-card closure (which would get a fresh identity every
  // TaskColumn render and defeat the memo).
  const handleDelete = useCallback(async (taskId: string) => {
    try {
      await api.tasks.delete(taskId)
      remove(taskId)
    } catch (e) {
      console.error('Failed to delete task:', e)
    }
  }, [remove])

  // Same reasoning for multi-select: pass the ordered visible-task ids as a
  // stable array (memoized alongside visibleTasks upstream isn't necessary
  // here — onToggleSelect itself already takes taskId directly) and let
  // TaskCard call onToggleSelect(taskId, shiftKey); the column supplies the
  // rest of the ordered-ids context via a ref-free wrapper keyed only on the
  // stable onToggleSelect prop and the current visibleTasks identity.
  const handleToggleSelect = useCallback(
    (taskId: string, shiftKey: boolean) => {
      onToggleSelect?.(taskId, visibleTasks.map((t) => t.id), shiftKey)
    },
    [onToggleSelect, visibleTasks],
  )

  return (
    <div className={`flex flex-col shrink-0${className ? ` ${className}` : ' w-72'}`}>
      <div className="flex items-center justify-between px-3 py-2 mb-2">
        <span className={`text-sm font-semibold uppercase tracking-wide ${isAtOrOverLimit ? (isHardAtOrOverLimit ? 'text-red-300' : 'text-amber-300') : 'text-slate-300'}`}>
          {label.name}
        </span>
        <span className={badgeClassName} title={badgeTitle} data-testid="column-count-badge">
          {wipLimit != null ? `${tasks.length} / ${wipLimit}` : tasks.length}
        </span>
      </div>
      <div
        ref={setNodeRef}
        className={`flex-1 flex flex-col gap-3 p-2 rounded-lg min-h-[100px] transition-colors ${isOver ? 'bg-slate-700/50' : 'bg-slate-800/30'}`}
      >
        {visibleTasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
            rateLimitedUntil={rateLimitedTaskIds?.get(task.id)}
            costWarned={costWarnedTaskIds?.has(task.id)}
            onDelete={handleDelete}
            onDuplicate={onDuplicate}
            isEditable={isStartingColumn}
            selected={selectedIds?.has(task.id)}
            onToggleSelect={onToggleSelect ? handleToggleSelect : undefined}
          />
        ))}
        {tasks.length === 0 && (
          <div className="text-center text-slate-600 text-sm py-8">No tasks</div>
        )}
        {shouldCollapse && (
          <button
            onClick={() => setExpanded(!expanded)}
            className="w-full text-xs text-slate-500 hover:text-slate-300 border border-dashed border-slate-700 hover:border-slate-500 rounded-lg py-2 mt-1 transition-colors"
          >
            {expanded ? '▲ Show less' : `▼ Show ${tasks.length - MAX_VISIBLE} more`}
          </button>
        )}
        {onAddTask && (
          <button
            onClick={onAddTask}
            className="w-full text-sm text-slate-500 hover:text-slate-300 border border-dashed border-slate-700 hover:border-slate-500 rounded-lg py-2 transition-colors"
          >
            + Add task
          </button>
        )}
      </div>
    </div>
  )
}
