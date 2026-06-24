import type { Task, WorkflowLabel } from '../../api/client'
import TaskColumn from './TaskColumn'

type Props = {
  labels: WorkflowLabel[]
  tasks: Task[]
  runningTaskIds: Set<string>
}

export default function TaskBoard({ labels, tasks, runningTaskIds }: Props) {
  const byLabel = (name: string) => tasks.filter((t) => t.label === name)

  return (
    <div className="flex gap-5 overflow-x-auto pb-4">
      {labels.map((label) => (
        <TaskColumn
          key={label.id}
          label={label}
          tasks={byLabel(label.name)}
          runningTaskIds={runningTaskIds}
        />
      ))}
    </div>
  )
}
