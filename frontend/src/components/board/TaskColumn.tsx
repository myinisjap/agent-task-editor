import { useState } from 'react'
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
  onAddTask?: () => void
  isStartingColumn?: boolean
  isTerminal?: boolean
  className?: string
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
}

export default function TaskColumn({ label, tasks, runningTaskIds, rateLimitedTaskIds, onAddTask, isStartingColumn, isTerminal, className, selectedIds, onToggleSelect }: Props) {
  const { setNodeRef, isOver } = useDroppable({ id: label.name })
  const { remove } = useTasksStore()
  const [expanded, setExpanded] = useState(false)

  const shouldCollapse = !!isTerminal && tasks.length > MAX_VISIBLE
  const visibleTasks = shouldCollapse && !expanded ? tasks.slice(0, MAX_VISIBLE) : tasks

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
      <div className="flex items-center justify-between px-3 py-2 mb-2">
        <span className="text-sm font-semibold text-slate-300 uppercase tracking-wide">{label.name}</span>
        <span className="text-xs text-slate-500 bg-slate-800 rounded-full px-2 py-0.5">{tasks.length}</span>
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
            onDelete={() => handleDelete(task.id)}
            isEditable={isStartingColumn}
            selected={selectedIds?.has(task.id)}
            onToggleSelect={
              onToggleSelect &&
              ((taskId, shiftKey) => onToggleSelect(taskId, visibleTasks.map((t) => t.id), shiftKey))
            }
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
