import { labelDisplayName } from '../../lib/labelDisplay'
import { useState, useEffect, useMemo } from 'react'
import {
  DndContext,
  KeyboardCode,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import type { DragEndEvent, DragStartEvent } from '@dnd-kit/core'
import type { Task, WorkflowLabel, WorkflowTransition } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import TaskColumn from './TaskColumn'
import AgentGroupColumn from './AgentGroupColumn'
import { computeCondensedGroups } from '../../lib/condensedBoard'
import { useIsMobile } from '../../lib/useIsMobile'
import { useSearchParams } from 'react-router-dom'
import { useNeedsMeStore } from '../../stores/needsMe'
import { useColumnSwipe } from './columnSwipe'
import ColumnChips from './ColumnChips'
import type { ColumnChip } from './ColumnChips'
import { canHumanMove } from '../../lib/humanMoves'
import type { DropState } from './dropState'

// Shared empty array so byLabel()/byLabels() returns a referentially-stable
// value for labels with no tasks, instead of a fresh [] every render (which
// would defeat memo(TaskCard)'s parent-level React.memo equivalents and
// cause empty columns to still "change" on every render).
const EMPTY_TASKS: Task[] = []

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
  chips,
}: {
  currentIndex: number
  total: number
  label: string
  onPrev: () => void
  onNext: () => void
  onDotClick: (i: number) => void
  chips: ColumnChip[]
}) {
  return (
    <div className="mb-3">
      <ColumnChips chips={chips} currentIndex={currentIndex} onSelect={onDotClick} />
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
  costWarnedTaskIds?: Set<string>
  onAddTask?: () => void
  onDuplicate?: (task: Task) => void
  condensed?: boolean
  transitions?: WorkflowTransition[]
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
}

function TaskBoardInner({
  labels,
  tasks,
  runningTaskIds,
  rateLimitedTaskIds,
  costWarnedTaskIds,
  onAddTask,
  onDuplicate,
  condensed = false,
  transitions = [],
  selectedIds,
  onToggleSelect,
  onMoveError,
}: Props & { onMoveError: (message: string) => void }) {
  const upsert = useTasksStore((s) => s.upsert)
  const isMobile = useIsMobile()

  // Label of the card currently being dragged (null when idle); drives the
  // legal/illegal drop-column highlighting.
  const [dragFromLabel, setDragFromLabel] = useState<string | null>(null)

  // Require 5px movement to start a drag so clicks still navigate. KeyboardSensor
  // uses its default coordinate getter (arrow keys) — the board uses
  // useDraggable/useDroppable rather than useSortable, so no custom
  // sortableKeyboardCoordinates is needed. Key mapping on a focused card:
  // Space picks up/drops, Enter opens the task (TaskCard's onKeyDown). By
  // default dnd-kit treats both Space AND Enter as pick-up/drop, which would
  // swallow Enter before the card's own onKeyDown could use it to navigate —
  // restrict "start"/"end" to Space only so the two don't collide.
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 5 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 200, tolerance: 5 } }),
    useSensor(KeyboardSensor, {
      keyboardCodes: {
        start: [KeyboardCode.Space],
        cancel: [KeyboardCode.Esc],
        end: [KeyboardCode.Space],
      },
    }),
  )

  // Bucket tasks by label once per `tasks` change instead of re-filtering
  // the full list per column, per render (O(labels × tasks) previously).
  const tasksByLabel = useMemo(() => {
    const m = new Map<string, Task[]>()
    for (const t of tasks) {
      const key = t.label ?? ''
      const bucket = m.get(key)
      if (bucket) {
        bucket.push(t)
      } else {
        m.set(key, [t])
      }
    }
    return m
  }, [tasks])

  const byLabel = (name: string) => tasksByLabel.get(name) ?? EMPTY_TASKS
  const byLabels = (names: string[]) => names.flatMap((n) => tasksByLabel.get(n) ?? EMPTY_TASKS)

  // Labels an agent can pick tasks up from: some agent/both transition leaves
  // the label and the label itself isn't agent_ignore. Dropping a blocked task
  // into one of these is the case that warrants a heads-up — the move won't
  // dispatch until the blockers clear.
  const agentTriggerableLabels = new Set(
    labels
      .filter(
        (l) =>
          !l.agent_ignore &&
          transitions.some(
            (t) => t.from_label === l.name && (t.trigger_type === 'agent' || t.trigger_type === 'both'),
          ),
      )
      .map((l) => l.name),
  )

  // Drag feedback for the column that drops onto `dropLabel`. The dragged
  // card's own column gets no styling; every other column is either a legal
  // human-move target (highlighted) or not (dimmed).
  const dropStateFor = (dropLabel: string): DropState | undefined => {
    if (dragFromLabel == null || dropLabel === dragFromLabel) return undefined
    return canHumanMove(transitions, dragFromLabel, dropLabel) ? 'legal' : 'illegal'
  }

  function handleDragStart(event: DragStartEvent) {
    const task = tasks.find((t) => t.id === String(event.active.id))
    setDragFromLabel(task?.label ?? null)
  }

  function handleDragCancel() {
    setDragFromLabel(null)
  }

  function handleDragEnd(event: DragEndEvent) {
    setDragFromLabel(null)
    const { active, over } = event
    if (!over) return

    const taskId = String(active.id)
    const toLabel = String(over.id)
    const task = tasks.find((t) => t.id === taskId)
    if (!task || task.label === toLabel) return

    // Moving a blocked task into an agent-triggerable column doesn't erase the
    // block — it just sits un-dispatched until its dependencies satisfy. Confirm
    // so that's a deliberate choice, not a surprise.
    const blockedCount = task.blocked_by_count ?? 0
    if (blockedCount > 0 && agentTriggerableLabels.has(toLabel)) {
      const ok = window.confirm(
        `This task is blocked by ${blockedCount} unfinished dependency task${blockedCount === 1 ? '' : 's'}. ` +
          `Moving it to "${toLabel}" won't start an agent until those finish. Move anyway?`,
      )
      if (!ok) return
    }

    // The workflow has no transition for this move: the server would reject it
    // anyway, so don't move the card optimistically — just say why.
    if (!canHumanMove(transitions, task.label, toLabel)) {
      onMoveError(`Can't move "${task.title}" from "${task.label}" to "${toLabel}": no transition defined between these labels.`)
      return
    }

    const snapshot = { ...task }

    // Optimistic update
    upsert({ ...task, label: toLabel })

    api.tasks.moveLabel(taskId, toLabel).catch((e: unknown) => {
      // Snap back on engine rejection and surface the server's reason
      upsert(snapshot)
      onMoveError(e instanceof Error && e.message ? e.message : 'Failed to move task')
    })
  }

  const sortedLabels = [...labels].sort((a, b) => a.sort_order - b.sort_order)

  const groups = condensed && transitions.length > 0 ? computeCondensedGroups(labels, transitions) : []

  // Selected mobile column lives in the URL (?col=<label name>; for condensed
  // groups, any label in the group) so Back and reload keep your place.
  const [searchParams, setSearchParams] = useSearchParams()
  const colParam = searchParams.get('col')
  const needsMeByTask = useNeedsMeStore((s) => s.byTask)

  const columnItems: { key: string; label: string; names: string[] }[] =
    condensed && transitions.length > 0
      ? groups.map((g) =>
          g.kind === 'single'
            ? { key: g.label.name, label: labelDisplayName(g.label), names: [g.label.name] }
            : { key: g.labels[0]?.name ?? '', label: g.labels.map(labelDisplayName).join(' · '), names: g.labels.map((l) => l.name) },
        )
      : sortedLabels.map((l) => ({ key: l.name, label: labelDisplayName(l), names: [l.name] }))

  const chips: ColumnChip[] = columnItems.map((c) => {
    const ts = c.names.flatMap((n) => tasksByLabel.get(n) ?? EMPTY_TASKS)
    return {
      key: c.key,
      label: c.label,
      count: ts.length,
      needsHuman: ts.some((t) => !!needsMeByTask[t.id] || t.active_run_status === 'waiting_human'),
    }
  })

  const found = colParam == null ? -1 : columnItems.findIndex((c) => c.names.includes(colParam))
  const currentIdx = Math.max(0, found)

  const selectColumn = (i: number) => {
    const idx = Math.max(0, Math.min(columnItems.length - 1, i))
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (idx === 0) next.delete('col')
        else next.set('col', columnItems[idx].key)
        return next
      },
      { replace: true },
    )
  }

  const clampedNormal = currentIdx
  const clampedCondensed = currentIdx

  // Swipe pages columns; disabled mid-drag so a long-press drag released after
  // a sideways move doesn't also change column.
  const swipe = useColumnSwipe({
    onSwipeLeft: () => selectColumn(currentIdx + 1),
    onSwipeRight: () => selectColumn(currentIdx - 1),
    disabled: dragFromLabel != null,
  })
  const condensedSwipe = swipe
  const normalSwipe = swipe

  if (condensed && transitions.length > 0) {
    if (isMobile && groups.length > 0) {
      const currentGroup = groups[clampedCondensed]
      const currentLabel =
        currentGroup.kind === 'single'
          ? currentGroup.label.name
          : 'Agent'

      return (
        <DndContext sensors={sensors} onDragStart={handleDragStart} onDragCancel={handleDragCancel} onDragEnd={handleDragEnd}>
          <div className="flex flex-col h-full">
            <MobileColumnNav
              currentIndex={clampedCondensed}
              total={groups.length}
              label={currentLabel}
              onPrev={() => selectColumn(clampedCondensed - 1)}
              onNext={() => selectColumn(clampedCondensed + 1)}
              onDotClick={selectColumn}
              chips={chips}
            />
            <div
              className="flex-1 min-h-0 overflow-y-auto"
              style={{ touchAction: 'pan-y' }}
              onTouchStart={condensedSwipe.onTouchStart}
              onTouchEnd={condensedSwipe.onTouchEnd}
              onTouchCancel={condensedSwipe.onTouchCancel}
            >
              {currentGroup.kind === 'single' ? (
                <TaskColumn
                  label={currentGroup.label}
                  tasks={byLabel(currentGroup.label.name)}
                  runningTaskIds={runningTaskIds}
                  rateLimitedTaskIds={rateLimitedTaskIds}
                  costWarnedTaskIds={costWarnedTaskIds}
                  selectedIds={selectedIds}
                  onToggleSelect={onToggleSelect}
                  onDuplicate={onDuplicate}
                  onAddTask={clampedCondensed === 0 ? onAddTask : undefined}
                  isStartingColumn={clampedCondensed === 0}
                  isTerminal={!!currentGroup.label.is_terminal}
                  dropState={dropStateFor(currentGroup.label.name)}
                  className="w-full"
                />
              ) : (
                <AgentGroupColumn
                  labels={currentGroup.labels}
                  tasks={byLabels(currentGroup.labels.map((l) => l.name))}
                  runningTaskIds={runningTaskIds}
                  rateLimitedTaskIds={rateLimitedTaskIds}
                  costWarnedTaskIds={costWarnedTaskIds}
                  selectedIds={selectedIds}
                  onToggleSelect={onToggleSelect}
                  onDuplicate={onDuplicate}
                  dropState={dropStateFor(currentGroup.labels[0]?.name ?? '')}
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
      <DndContext sensors={sensors} onDragStart={handleDragStart} onDragCancel={handleDragCancel} onDragEnd={handleDragEnd}>
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
                  costWarnedTaskIds={costWarnedTaskIds}
                  selectedIds={selectedIds}
                  onToggleSelect={onToggleSelect}
                  onDuplicate={onDuplicate}
                  onAddTask={i === 0 ? onAddTask : undefined}
                  isStartingColumn={i === 0}
                  isTerminal={!!group.label.is_terminal}
                  dropState={dropStateFor(group.label.name)}
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
                  costWarnedTaskIds={costWarnedTaskIds}
                  selectedIds={selectedIds}
                  onToggleSelect={onToggleSelect}
                  onDuplicate={onDuplicate}
                  dropState={dropStateFor(group.labels[0]?.name ?? '')}
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
      <DndContext sensors={sensors} onDragStart={handleDragStart} onDragCancel={handleDragCancel} onDragEnd={handleDragEnd}>
        <div className="flex flex-col h-full">
          <MobileColumnNav
            currentIndex={clampedNormal}
            total={sortedLabels.length}
            label={currentLabel.name}
            onPrev={() => selectColumn(clampedNormal - 1)}
            onNext={() => selectColumn(clampedNormal + 1)}
            onDotClick={selectColumn}
            chips={chips}
          />
          <div
            className="flex-1 min-h-0 overflow-y-auto"
            style={{ touchAction: 'pan-y' }}
            onTouchStart={normalSwipe.onTouchStart}
            onTouchEnd={normalSwipe.onTouchEnd}
            onTouchCancel={normalSwipe.onTouchCancel}
          >
            <TaskColumn
              label={currentLabel}
              tasks={byLabel(currentLabel.name)}
              runningTaskIds={runningTaskIds}
              rateLimitedTaskIds={rateLimitedTaskIds}
              costWarnedTaskIds={costWarnedTaskIds}
              selectedIds={selectedIds}
              onToggleSelect={onToggleSelect}
              onDuplicate={onDuplicate}
              onAddTask={clampedNormal === 0 ? onAddTask : undefined}
              isStartingColumn={clampedNormal === 0}
              isTerminal={!!currentLabel.is_terminal}
              dropState={dropStateFor(currentLabel.name)}
              className="w-full"
            />
          </div>
        </div>
      </DndContext>
    )
  }

  // Desktop normal (expanded) view
  return (
    <DndContext sensors={sensors} onDragStart={handleDragStart} onDragCancel={handleDragCancel} onDragEnd={handleDragEnd}>
      <div className="flex gap-5 overflow-x-auto h-full pb-4">
        {sortedLabels.map((label, i) => (
          <TaskColumn
            key={label.id}
            label={label}
            tasks={byLabel(label.name)}
            runningTaskIds={runningTaskIds}
            rateLimitedTaskIds={rateLimitedTaskIds}
            costWarnedTaskIds={costWarnedTaskIds}
            selectedIds={selectedIds}
            onToggleSelect={onToggleSelect}
            onDuplicate={onDuplicate}
            onAddTask={i === 0 ? onAddTask : undefined}
            isStartingColumn={i === 0}
            isTerminal={!!label.is_terminal}
            dropState={dropStateFor(label.name)}
          />
        ))}
      </div>
    </DndContext>
  )
}

// How long the rejected-move notice stays up before dismissing itself.
const MOVE_ERROR_MS = 6000

export default function TaskBoard(props: Props) {
  const [moveError, setMoveError] = useState<string | null>(null)

  useEffect(() => {
    if (!moveError) return
    const timer = setTimeout(() => setMoveError(null), MOVE_ERROR_MS)
    return () => clearTimeout(timer)
  }, [moveError])

  return (
    <>
      <TaskBoardInner {...props} onMoveError={setMoveError} />
      {moveError && (
        <div
          role="alert"
          className="fixed bottom-4 right-4 z-50 max-w-sm flex items-start gap-3 rounded-lg border border-red-700 bg-red-950 px-4 py-3 text-sm text-red-200 shadow-lg"
        >
          <span className="flex-1">{moveError}</span>
          <button
            type="button"
            onClick={() => setMoveError(null)}
            aria-label="Dismiss"
            className="text-red-300 hover:text-red-100"
          >
            ✕
          </button>
        </div>
      )}
    </>
  )
}
