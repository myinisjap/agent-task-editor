import { useDroppable } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import TaskCard from './TaskCard'

type Props = {
  labels: WorkflowLabel[]
  tasks: Task[]
  runningTaskIds: Set<string>
  rateLimitedTaskIds?: Map<string, string>
  className?: string
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
}

/**
 * Renders a collapsed agent-only group of columns as a single board column.
 *
 * - No named header — just a subtle ⚙ icon and a task count badge.
 * - Tasks show a small label badge indicating which actual column they're in.
 * - Dropping a task onto this column moves it to the first label in the group.
 */
export default function AgentGroupColumn({ labels, tasks, runningTaskIds, rateLimitedTaskIds, className, selectedIds, onToggleSelect }: Props) {
  // Use the first label's name as the droppable id so DnD moves tasks here
  const dropId = labels[0]?.name ?? '__agent-group__'
  const { setNodeRef, isOver } = useDroppable({ id: dropId })
  const { remove } = useTasksStore()

  const handleDelete = async (taskId: string) => {
    try {
      await api.tasks.delete(taskId)
      remove(taskId)
    } catch (e) {
      console.error('Failed to delete task:', e)
    }
  }

  return (
    <div className={`flex flex-col shrink-0${className ? ` ${className}` : ' w-72'}`}>
      {/* Minimal header — no label name */}
      <div className="flex items-center justify-between px-3 py-2 mb-2">
        <span className="text-xs text-slate-600 flex items-center gap-1.5">
          <span>⚙</span>
          <span className="uppercase tracking-wide font-semibold">agent</span>
        </span>
        <span className="text-xs text-slate-500 bg-slate-800 rounded-full px-2 py-0.5">{tasks.length}</span>
      </div>

      <div
        ref={setNodeRef}
        className={`flex-1 flex flex-col gap-3 p-2 rounded-lg min-h-[100px] transition-colors ${
          isOver ? 'bg-slate-700/50' : 'bg-slate-800/30'
        }`}
      >
        {tasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
            rateLimitedUntil={rateLimitedTaskIds?.get(task.id)}
            onDelete={() => handleDelete(task.id)}
            showColumnLabel={task.label}
            selected={selectedIds?.has(task.id)}
            onToggleSelect={
              onToggleSelect &&
              ((taskId, shiftKey) => onToggleSelect(taskId, tasks.map((t) => t.id), shiftKey))
            }
          />
        ))}
        {tasks.length === 0 && (
          <div className="text-center text-slate-600 text-sm py-8">No tasks</div>
        )}
      </div>
    </div>
  )
}
