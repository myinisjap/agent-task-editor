import type { Task } from '../../api/client'
import type { WorkflowLabel } from '../../api/client'
import TaskCard from './TaskCard'

type Props = {
  label: WorkflowLabel
  tasks: Task[]
  runningTaskIds: Set<string>
}

export default function TaskColumn({ label, tasks, runningTaskIds }: Props) {
  const isIgnored = label.agent_ignore === 1

  return (
    <div className="flex flex-col w-64 shrink-0">
      <div
        className="flex items-center gap-2 mb-3 px-1"
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

      <div className="flex flex-col gap-2">
        {tasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
          />
        ))}
        {tasks.length === 0 && (
          <div className="text-xs text-slate-600 text-center py-4 border border-dashed border-slate-700 rounded-lg">
            empty
          </div>
        )}
      </div>
    </div>
  )
}
