import { useCallback } from 'react'
import { useDroppable } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { labelDisplayName, labelDisplayNameByName } from '../../lib/labelDisplay'
import TaskCard from './TaskCard'
import { dropStateClass, type DropState } from './dropState'

type Props = {
  labels: WorkflowLabel[]
  tasks: Task[]
  runningTaskIds: Set<string>
  rateLimitedTaskIds?: Map<string, string>
  costWarnedTaskIds?: Set<string>
  onDuplicate?: (task: Task) => void
  className?: string
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
  /** While a card is being dragged: whether this column is a legal drop target. */
  dropState?: DropState
}

/**
 * Renders a collapsed agent-only group of columns as a single board column.
 *
 * - Header: a subtle ⚙ icon, the grouped labels' display names, and a task count badge.
 * - Tasks show a small label badge indicating which actual column they're in.
 * - Dropping a task onto this column moves it to the first label in the group.
 */
export default function AgentGroupColumn({ labels, tasks, runningTaskIds, rateLimitedTaskIds, costWarnedTaskIds, onDuplicate, className, selectedIds, onToggleSelect, dropState }: Props) {
  // Use the first label's name as the droppable id so DnD moves tasks here
  const dropId = labels[0]?.name ?? '__agent-group__'
  const { setNodeRef, isOver } = useDroppable({ id: dropId })
  const remove = useTasksStore((s) => s.remove)
  const groupTitle = labels.map(labelDisplayName).join(' · ')

  const handleDelete = useCallback(async (taskId: string) => {
    try {
      await api.tasks.delete(taskId)
      remove(taskId)
    } catch (e) {
      console.error('Failed to delete task:', e)
      alert(String(e))
    }
  }, [remove])

  const handleToggleSelect = useCallback(
    (taskId: string, shiftKey: boolean) => {
      onToggleSelect?.(taskId, tasks.map((t) => t.id), shiftKey)
    },
    [onToggleSelect, tasks],
  )

  return (
    <div className={`flex flex-col shrink-0${className ? ` ${className}` : ' w-72'}`}>
      <div className="flex items-center justify-between px-3 py-2 mb-2">
        <span className="text-xs text-slate-500 flex items-center gap-1.5 min-w-0" title={groupTitle}>
          <span>⚙</span>
          <span className="font-semibold truncate">{groupTitle}</span>
        </span>
        <span className="text-xs text-slate-500 bg-slate-800 rounded-full px-2 py-0.5">{tasks.length}</span>
      </div>

      <div
        ref={setNodeRef}
        className={`flex-1 flex flex-col gap-3 p-2 rounded-lg min-h-[100px] transition-colors ${isOver && dropState !== 'illegal' ? 'bg-slate-700/50' : 'bg-slate-800/30'} ${dropStateClass(dropState)}`}
      >
        {tasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
            rateLimitedUntil={rateLimitedTaskIds?.get(task.id)}
            costWarned={costWarnedTaskIds?.has(task.id)}
            onDelete={handleDelete}
            onDuplicate={onDuplicate}
            showColumnLabel={labelDisplayNameByName(task.label, labels)}
            selected={selectedIds?.has(task.id)}
            onToggleSelect={onToggleSelect ? handleToggleSelect : undefined}
          />
        ))}
        {tasks.length === 0 && (
          <div className="text-center text-slate-600 text-sm py-8">No tasks</div>
        )}
      </div>
    </div>
  )
}
