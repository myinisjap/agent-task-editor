import { DndContext, MouseSensor, TouchSensor, useSensor, useSensors } from '@dnd-kit/core'
import type { DragEndEvent } from '@dnd-kit/core'
import type { Task, WorkflowLabel, WorkflowTransition } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import TaskColumn from './TaskColumn'
import AgentGroupColumn from './AgentGroupColumn'
import { computeCondensedGroups } from '../../lib/condensedBoard'

type Props = {
  labels: WorkflowLabel[]
  tasks: Task[]
  runningTaskIds: Set<string>
  rateLimitedTaskIds?: Map<string, string>
  onAddTask?: () => void
  condensed?: boolean
  transitions?: WorkflowTransition[]
}

export default function TaskBoard({
  labels,
  tasks,
  runningTaskIds,
  rateLimitedTaskIds,
  onAddTask,
  condensed = false,
  transitions = [],
}: Props) {
  const { upsert } = useTasksStore()

  // Require 5px movement to start a drag so clicks still navigate
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 5 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 200, tolerance: 5 } }),
  )

  const byLabel = (name: string) => tasks.filter((t) => t.label === name)
  const byLabels = (names: string[]) => tasks.filter((t) => names.includes(t.label ?? ''))

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

  const sortedLabels = [...labels].sort((a, b) => a.sort_order - b.sort_order)

  if (condensed && transitions.length > 0) {
    const groups = computeCondensedGroups(labels, transitions)

    return (
      <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
        <div className="flex gap-5 overflow-x-auto h-full pb-4">
          {groups.map((group, i) => {
            if (group.kind === 'single') {
              return (
                <TaskColumn
                  key={group.label.id}
                  label={group.label}
                  tasks={byLabel(group.label.name)}
                  runningTaskIds={runningTaskIds}
                  rateLimitedTaskIds={rateLimitedTaskIds}
                  onAddTask={i === 0 ? onAddTask : undefined}
                  isStartingColumn={i === 0}
                />
              )
            } else {
              // agent-group: collapse into a single column
              const groupKey = group.labels.map((l) => l.id).join('-')
              const groupTasks = byLabels(group.labels.map((l) => l.name))
              return (
                <AgentGroupColumn
                  key={groupKey}
                  labels={group.labels}
                  tasks={groupTasks}
                  runningTaskIds={runningTaskIds}
                  rateLimitedTaskIds={rateLimitedTaskIds}
                />
              )
            }
          })}
        </div>
      </DndContext>
    )
  }

  // Normal (expanded) view
  return (
    <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
      <div className="flex gap-5 overflow-x-auto h-full pb-4">
        {sortedLabels.map((label, i) => (
          <TaskColumn
            key={label.id}
            label={label}
            tasks={byLabel(label.name)}
            runningTaskIds={runningTaskIds}
            rateLimitedTaskIds={rateLimitedTaskIds}
            onAddTask={i === 0 ? onAddTask : undefined}
            isStartingColumn={i === 0}
          />
        ))}
      </div>
    </DndContext>
  )
}
