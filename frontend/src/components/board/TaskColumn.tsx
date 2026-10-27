import { labelDisplayName } from '../../lib/labelDisplay'
import { useCallback, useMemo, useState } from 'react'
import { useDroppable } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { useAgentsStore } from '../../stores/agents'
import { useWorkflowStore } from '../../stores/workflow'
import { unstaffedLabelNames } from '../../lib/staffing'
import TaskCard from './TaskCard'
import { dropStateClass, type DropState } from './dropState'
import { columnWidthClass, type ColumnMode } from '../../lib/boardLayout'

const MAX_VISIBLE = 5

type Props = {
  label: WorkflowLabel
  tasks: Task[]
  runningTaskIds: Set<string>
  rateLimitedTaskIds?: Map<string, string>
  costWarnedTaskIds?: Set<string>
  onAddTask?: () => void
  onDuplicate?: (task: Task) => void
  isStartingColumn?: boolean
  isTerminal?: boolean
  className?: string
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
  /** While a card is being dragged: whether this column is a legal drop target. */
  dropState?: DropState
  /** Desktop width mode; defaults to 'full'. */
  mode?: ColumnMode
  /** Expand/collapse a terminal column (persisted by the board). */
  onToggleCollapsed?: () => void
  /** Droppable id override (swimlanes prefix ids); the label name travels in data. */
  dropId?: string
  /** A card is being dragged somewhere on the board. */
  dragging?: boolean
}

export default function TaskColumn({ label, tasks, runningTaskIds, rateLimitedTaskIds, costWarnedTaskIds, onAddTask, onDuplicate, isStartingColumn, isTerminal, className, selectedIds, onToggleSelect, dropState, mode = 'full', onToggleCollapsed, dropId, dragging }: Props) {
  const { setNodeRef, isOver } = useDroppable({ id: dropId ?? label.name, data: { label: label.name } })
  const [hovered, setHovered] = useState(false)
  const expandedTransient = mode !== 'full' && ((hovered && !dragging) || isOver)
  const compact = mode !== 'full' && !expandedTransient
  const displayName = labelDisplayName(label)
  const remove = useTasksStore((s) => s.remove)
  const [expanded, setExpanded] = useState(false)

  // Flag agent-triggerable columns nobody works. Only once agent configs have
  // loaded, so the chip doesn't flash on a cold board.
  const agents = useAgentsStore((s) => s.configs)
  const agentsLoaded = useAgentsStore((s) => s.loaded)
  const workflows = useWorkflowStore((s) => s.workflows)
  const selectedWorkflowId = useWorkflowStore((s) => s.selectedId)
  const unstaffed = useMemo(() => {
    if (!agentsLoaded) return false
    const wf = workflows.find((w) => w.id === (label.workflow_id || selectedWorkflowId)) ?? workflows[0]
    return unstaffedLabelNames(wf, agents).has(label.name)
  }, [agentsLoaded, workflows, selectedWorkflowId, agents, label.workflow_id, label.name])

  const shouldCollapse = !!isTerminal && tasks.length > MAX_VISIBLE
  const visibleTasks = shouldCollapse && !expanded ? tasks.slice(0, MAX_VISIBLE) : tasks

  const wipLimit = label.wip_limit != null && label.wip_limit > 0 ? label.wip_limit : null
  // >= matches the dispatcher's enforcement point: a hard-limited column is
  // already actively blocking new dispatch once it *reaches* its limit, not
  // only once it exceeds it (see checkWIPLimit in backend/internal/agent/dispatcher.go).
  const isAtOrOverLimit = wipLimit != null && tasks.length >= wipLimit
  const isHardAtOrOverLimit = isAtOrOverLimit && label.wip_limit_hard !== 0
  const badgeClassName = isAtOrOverLimit
    ? isHardAtOrOverLimit
      ? 'text-xs text-red-300 bg-red-900/60 border border-red-700 rounded-full px-2 py-0.5'
      : 'text-xs text-amber-300 bg-amber-900/40 border border-amber-700 rounded-full px-2 py-0.5'
    : 'text-xs text-slate-500 bg-slate-800 rounded-full px-2 py-0.5'
  const badgeTitle = isHardAtOrOverLimit
    ? `At/over WIP limit (${tasks.length}/${wipLimit}) — dispatcher will hold new tasks back from this label until it drops below the limit`
    : isAtOrOverLimit
      ? `At/over WIP limit (${tasks.length}/${wipLimit}) — visual warning only, dispatch is not blocked`
      : undefined

  // Stable callback identity (keyed only on `remove`, not per-render) so
  // memo(TaskCard) below can actually skip re-rendering unrelated cards.
  // TaskCard calls onDelete(task.id) directly rather than the column handing
  // it a pre-bound per-card closure (which would get a fresh identity every
  // TaskColumn render and defeat the memo).
  const handleDelete = useCallback(async (taskId: string) => {
    try {
      await api.tasks.delete(taskId)
      remove(taskId)
    } catch (e) {
      console.error('Failed to delete task:', e)
      alert(String(e))
    }
  }, [remove])

  // Same reasoning for multi-select: pass the ordered visible-task ids as a
  // stable array (memoized alongside visibleTasks upstream isn't necessary
  // here — onToggleSelect itself already takes taskId directly) and let
  // TaskCard call onToggleSelect(taskId, shiftKey); the column supplies the
  // rest of the ordered-ids context via a ref-free wrapper keyed only on the
  // stable onToggleSelect prop and the current visibleTasks identity.
  const handleToggleSelect = useCallback(
    (taskId: string, shiftKey: boolean) => {
      onToggleSelect?.(taskId, visibleTasks.map((t) => t.id), shiftKey)
    },
    [onToggleSelect, visibleTasks],
  )

  const toggleBtn = isTerminal && onToggleCollapsed && !compact ? (
    <button
      type="button"
      onClick={onToggleCollapsed}
      aria-expanded={mode === 'full'}
      aria-label={`${mode === 'full' ? 'Collapse' : 'Expand'} ${displayName} column`}
      title={mode === 'full' ? 'Collapse column' : 'Expand column'}
      className="ml-2 text-xs text-slate-400 hover:text-slate-200"
    >
      {mode === 'full' ? '‹' : '›'}
    </button>
  ) : null

  return (
    <div
      data-column-mode={mode}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className={`flex flex-col shrink-0 transition-[width,flex-basis]${className ? ` ${className}` : ` ${columnWidthClass(mode, expandedTransient)}`}`}
    >
      <div className={`flex items-center ${compact ? 'justify-center px-0' : 'justify-between px-3'} py-2 mb-2`}>
        {!compact && (
          <span className={`text-sm font-semibold ${isAtOrOverLimit ? (isHardAtOrOverLimit ? 'text-red-300' : 'text-amber-300') : 'text-slate-300'}`}>
            {displayName}
          </span>
        )}
        {!compact && unstaffed && (
          <span
            data-testid="no-agent-chip"
            title="Tasks in this column are waiting for an agent, but no enabled agent config covers it. Assign one on the Pipeline page."
            className="ml-2 mr-auto text-[10px] font-medium normal-case tracking-normal text-amber-300 bg-amber-900/40 border border-amber-700 rounded-full px-1.5 py-0.5"
          >
            No agent assigned
          </span>
        )}
        <span className={compact ? badgeClassName.replace('px-2', 'px-1.5') : badgeClassName} title={badgeTitle} data-testid="column-count-badge">
          {wipLimit != null ? `${tasks.length} / ${wipLimit}` : tasks.length}
        </span>
        {toggleBtn}
      </div>
      <div
        ref={setNodeRef}
        className={`flex-1 flex flex-col ${compact ? 'items-center py-2' : 'gap-3 p-2'} rounded-lg min-h-[100px] transition-colors ${isOver && dropState !== 'illegal' ? 'bg-slate-700/50' : 'bg-slate-800/30'} ${dropStateClass(dropState)}`}
      >
        {compact && (
          isTerminal && onToggleCollapsed ? (
            <button
              type="button"
              onClick={onToggleCollapsed}
              aria-expanded={false}
              aria-label={`Expand ${displayName} column`}
              title={displayName}
              style={{ writingMode: 'vertical-rl' }}
              className="text-sm font-semibold text-slate-400 hover:text-slate-200"
            >
              {displayName}
            </button>
          ) : (
            <span title={displayName} style={{ writingMode: 'vertical-rl' }} className="text-sm font-semibold text-slate-400">
              {displayName}
            </span>
          )
        )}
        {!compact && visibleTasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
            rateLimitedUntil={rateLimitedTaskIds?.get(task.id)}
            costWarned={costWarnedTaskIds?.has(task.id)}
            onDelete={handleDelete}
            onDuplicate={onDuplicate}
            isEditable={isStartingColumn}
            selected={selectedIds?.has(task.id)}
            onToggleSelect={onToggleSelect ? handleToggleSelect : undefined}
          />
        ))}
        {!compact && tasks.length === 0 && (
          <div className="text-center text-slate-600 text-sm py-8">No tasks</div>
        )}
        {!compact && shouldCollapse && (
          <button
            onClick={() => setExpanded(!expanded)}
            className="w-full text-xs text-slate-500 hover:text-slate-300 border border-dashed border-slate-700 hover:border-slate-500 rounded-lg py-2 mt-1 transition-colors"
          >
            {expanded ? '▲ Show less' : `▼ Show ${tasks.length - MAX_VISIBLE} more`}
          </button>
        )}
        {!compact && onAddTask && (
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
