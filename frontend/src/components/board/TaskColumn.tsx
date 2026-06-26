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
  const isIgnored = label.agent_ignore === 1

  return (
    <div className="flex flex-col w-64 shrink-0">
      <div
        className="flex items-center gap-2 mb-3 px-1 pb-1"
        style={{ borderBottom: `2px solid ${label.color}` }}
      >
        <span
          className="w-2.5 h-2.5 rounded-full shrink-0"
          style={{ backgroundColor: label.color }}
        />
        <span className={`text-sm font-medium flex-1 ${isIgnored ? 'text-slate-500' : 'text-slate-200'}`}>
          {label.name}
        </span>
        <span className="text-xs text-slate-500 bg-slate-800 rounded px-1.5 py-0.5">
          {tasks.length}
        </span>
      </div>

      <div
        ref={setNodeRef}
        className={`flex flex-col gap-2 flex-1 min-h-12 rounded-lg transition-colors ${
          isOver ? 'bg-slate-800/50 ring-1 ring-slate-600' : ''
        }`}
      >
        {tasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
          />
        ))}
        {tasks.length === 0 && (
          <div className={`text-xs text-center py-4 border border-dashed rounded-lg ${
            isOver ? 'border-slate-500 text-slate-400' : 'border-slate-700 text-slate-600'
          }`}>
            {isOver ? 'drop here' : 'empty'}
          </div>
        )}
      </div>

      {onAddTask && (
        <button
          onClick={onAddTask}
          className="mt-3 w-full py-1.5 text-xs text-slate-500 hover:text-slate-300 hover:bg-slate-800 border border-dashed border-slate-700 hover:border-slate-600 rounded-lg transition-colors"
        >
          + New Task
        </button>
      )}
    </div>
  )
}
