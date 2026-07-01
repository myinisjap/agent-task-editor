import { useState, useEffect } from 'react'
import { DndContext, MouseSensor, TouchSensor, useSensor, useSensors } from '@dnd-kit/core'
import type { DragEndEvent } from '@dnd-kit/core'
import type { Task, WorkflowLabel, WorkflowTransition } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import TaskColumn from './TaskColumn'
import AgentGroupColumn from './AgentGroupColumn'
import { computeCondensedGroups } from '../../lib/condensedBoard'
import { useIsMobile } from '../../lib/useIsMobile'

// Extracted to module level so React sees a stable component type across renders.
// If defined inside TaskBoard's function body, React would create a new type on
// every parent render, causing it to unmount/remount and destroy child state.
function MobileColumnNav({
  currentIndex,
  total,
  label,
  onPrev,
  onNext,
  onDotClick,
}: {
  currentIndex: number
  total: number
  label: string
  onPrev: () => void
  onNext: () => void
  onDotClick: (i: number) => void
}) {
  return (
    <div className="mb-3">
      <div className="flex items-center justify-between mb-2">
        <button
          disabled={currentIndex === 0}
          onClick={onPrev}
          className="px-3 py-1.5 text-sm rounded bg-slate-800 text-slate-300 disabled:opacity-30 active:bg-slate-700"
        >
          ◀
        </button>
        <span className="text-sm font-semibold text-slate-200">
          {label} ({currentIndex + 1}/{total})
        </span>
        <button
          disabled={currentIndex === total - 1}
          onClick={onNext}
          className="px-3 py-1.5 text-sm rounded bg-slate-800 text-slate-300 disabled:opacity-30 active:bg-slate-700"
        >
          ▶
        </button>
      </div>
      <div className="flex justify-center gap-1.5">
        {Array.from({ length: total }).map((_, i) => (
          <button
            key={i}
            onClick={() => onDotClick(i)}
            className={`w-2 h-2 rounded-full transition-colors ${
              i === currentIndex ? 'bg-indigo-400' : 'bg-slate-600'
            }`}
          />
        ))}
      </div>
    </div>
  )
}

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
  const isMobile = useIsMobile()

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

  // Mobile column index state — separate for normal and condensed views
  const [mobileNormalIndex, setMobileNormalIndex] = useState(0)
  const [mobileCondensedIndex, setMobileCondensedIndex] = useState(0)

  // Clamp indices when label list changes
  const groups = condensed && transitions.length > 0 ? computeCondensedGroups(labels, transitions) : []
  const clampedNormal = Math.min(mobileNormalIndex, Math.max(0, sortedLabels.length - 1))
  const clampedCondensed = Math.min(mobileCondensedIndex, Math.max(0, groups.length - 1))

  // Keep state in sync after clamping
  useEffect(() => {
    if (clampedNormal !== mobileNormalIndex) setMobileNormalIndex(clampedNormal)
  }, [clampedNormal, mobileNormalIndex])

  useEffect(() => {
    if (clampedCondensed !== mobileCondensedIndex) setMobileCondensedIndex(clampedCondensed)
  }, [clampedCondensed, mobileCondensedIndex])

  if (condensed && transitions.length > 0) {
    if (isMobile && groups.length > 0) {
      const currentGroup = groups[clampedCondensed]
      const currentLabel =
        currentGroup.kind === 'single'
          ? currentGroup.label.name
          : 'Agent'

      return (
        <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
          <div className="flex flex-col h-full">
            <MobileColumnNav
              currentIndex={clampedCondensed}
              total={groups.length}
              label={currentLabel}
              onPrev={() => setMobileCondensedIndex((i) => i - 1)}
              onNext={() => setMobileCondensedIndex((i) => i + 1)}
              onDotClick={setMobileCondensedIndex}
            />
            <div className="flex-1 min-h-0 overflow-y-auto">
              {currentGroup.kind === 'single' ? (
                <TaskColumn
                  label={currentGroup.label}
                  tasks={byLabel(currentGroup.label.name)}
                  runningTaskIds={runningTaskIds}
                  rateLimitedTaskIds={rateLimitedTaskIds}
                  onAddTask={clampedCondensed === 0 ? onAddTask : undefined}
                  isStartingColumn={clampedCondensed === 0}
                  isTerminal={!!currentGroup.label.is_terminal}
                  className="w-full"
                />
              ) : (
                <AgentGroupColumn
                  labels={currentGroup.labels}
                  tasks={byLabels(currentGroup.labels.map((l) => l.name))}
                  runningTaskIds={runningTaskIds}
                  rateLimitedTaskIds={rateLimitedTaskIds}
                  className="w-full"
                />
              )}
            </div>
          </div>
        </DndContext>
      )
    }

    // Desktop condensed view
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
                  isTerminal={!!group.label.is_terminal}
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

  // Mobile normal (expanded) view
  if (isMobile && sortedLabels.length > 0) {
    const currentLabel = sortedLabels[clampedNormal]
    return (
      <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
        <div className="flex flex-col h-full">
          <MobileColumnNav
            currentIndex={clampedNormal}
            total={sortedLabels.length}
            label={currentLabel.name}
            onPrev={() => setMobileNormalIndex((i) => i - 1)}
            onNext={() => setMobileNormalIndex((i) => i + 1)}
            onDotClick={setMobileNormalIndex}
          />
          <div className="flex-1 min-h-0 overflow-y-auto">
            <TaskColumn
              label={currentLabel}
              tasks={byLabel(currentLabel.name)}
              runningTaskIds={runningTaskIds}
              rateLimitedTaskIds={rateLimitedTaskIds}
              onAddTask={clampedNormal === 0 ? onAddTask : undefined}
              isStartingColumn={clampedNormal === 0}
              isTerminal={!!currentLabel.is_terminal}
              className="w-full"
            />
          </div>
        </div>
      </DndContext>
    )
  }

  // Desktop normal (expanded) view
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
            isTerminal={!!label.is_terminal}
          />
        ))}
      </div>
    </DndContext>
  )
}
