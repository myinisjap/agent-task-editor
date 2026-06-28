import { DndContext, MouseSensor, TouchSensor, useSensor, useSensors } from '@dnd-kit/core'
import type { DragEndEvent } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import TaskColumn from './TaskColumn'

type Props = {
  labels: WorkflowLabel[]
  tasks: Task[]
  runningTaskIds: Set<string>
  onAddTask?: () => void
}

export default function TaskBoard({ labels, tasks, runningTaskIds, onAddTask }: Props) {
  const { upsert } = useTasksStore()

  // Require 5px movement to start a drag so clicks still navigate
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 5 } }),
    useSensor(TouchSensor,  { activationConstraint: { delay: 200, tolerance: 5 } }),
  )

  const byLabel = (name: string) => tasks.filter((t) => t.label === name)

  function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event
    if (!over) return

    const taskId = String(active.id)
    const toLabel = String(over.id)
    const task = tasks.find((t) => t.id === taskId)
    if (!task || task.label === toLabel) return

    const snapshot = { ...task }

    // Optimistic update
    upsert({ ...task, label: toLabel })

    api.tasks.moveLabel(taskId, toLabel).catch(() => {
      // Snap back on engine rejection
      upsert(snapshot)
    })
  }

  return (
    <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
      <div className="flex gap-5 overflow-x-auto h-full pb-4">
        {labels.map((label, i) => (
          <TaskColumn
            key={label.id}
            label={label}
            tasks={byLabel(label.name)}
            runningTaskIds={runningTaskIds}
            onAddTask={i === 0 ? onAddTask : undefined}
            isStartingColumn={i === 0}
          />
        ))}
      </div>
    </DndContext>
  )
}
