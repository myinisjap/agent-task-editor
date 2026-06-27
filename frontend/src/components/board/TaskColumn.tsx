import { useDroppable } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import TaskCard from './TaskCard'

type Props = {
  label: WorkflowLabel
  tasks: Task[]
  runningTaskIds: Set<string>
  onAddTask?: () => void
}

export default function TaskColumn({ label, tasks, runningTaskIds, onAddTask }: Props) {
  const { setNodeRef, isOver } = useDroppable({ id: label.name })

  return (
    <div className="flex flex-col w-72 shrink-0">
      <div className="flex items-center justify-between px-3 py-2 mb-2">
        <span className="text-sm font-semibold text-slate-300 uppercase tracking-wide">{label.name}</span>
        <span className="text-xs text-slate-500 bg-slate-800 rounded-full px-2 py-0.5">{tasks.length}</span>
      </div>
      <div
        ref={setNodeRef}
        className={`flex-1 flex flex-col gap-3 p-2 rounded-lg min-h-[100px] transition-colors ${isOver ? 'bg-slate-700/50' : 'bg-slate-800/30'}`}
      >
        {tasks.map((task) => (
          <TaskCard key={task.id} task={task} isRunning={runningTaskIds.has(task.id)} />
        ))}
        {tasks.length === 0 && (
          <div className="text-center text-slate-600 text-sm py-8">No tasks</div>
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
