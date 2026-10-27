import { Bot } from 'lucide-react'
import Icon from '../ui/Icon'
import { useCallback, useState } from 'react'
import { useDroppable } from '@dnd-kit/core'
import type { Task, WorkflowLabel } from '../../api/client'
import { labelDisplayName, labelDisplayNameByName } from '../../lib/labelDisplay'
import TaskCard from './TaskCard'
import { useDeleteTaskConfirm } from './useDeleteTaskConfirm'
import { columnWidthClass, type ColumnMode } from '../../lib/boardLayout'
import { dropStateClass, type DropState } from './dropState'

type Props = {
  labels: WorkflowLabel[]
  tasks: Task[]
  runningTaskIds: Set<string>
  rateLimitedTaskIds?: Map<string, string>
  costWarnedTaskIds?: Set<string>
  onDuplicate?: (task: Task) => void
  className?: string
  selectedIds?: Set<string>
  onToggleSelect?: (taskId: string, orderedIds: string[], shiftKey?: boolean) => void
  /** While a card is being dragged: whether this column is a legal drop target. */
  dropState?: DropState
  mode?: 'full' | 'narrow'
  dropId?: string
  dragging?: boolean
}

/**
 * Renders a collapsed agent-only group of columns as a single board column.
 *
 * - Header: a subtle ⚙ icon, the grouped labels' display names, and a task count badge.
 * - Tasks show a small label badge indicating which actual column they're in.
 * - Dropping a task onto this column moves it to the first label in the group.
 */
export default function AgentGroupColumn({ labels, tasks, runningTaskIds, rateLimitedTaskIds, costWarnedTaskIds, onDuplicate, className, selectedIds, onToggleSelect, dropState, mode = 'full', dropId: dropIdProp, dragging }: Props) {
  // Use the first label's name as the droppable id so DnD moves tasks here
  const dropId = dropIdProp ?? labels[0]?.name ?? '__agent-group__'
  const { setNodeRef, isOver } = useDroppable({ id: dropId, data: { label: labels[0]?.name } })
  const [hovered, setHovered] = useState(false)
  const colMode: ColumnMode = mode
  const expandedTransient = mode !== 'full' && ((hovered && !dragging) || isOver)
  const compact = mode !== 'full' && !expandedTransient
  const groupTitle = labels.map(labelDisplayName).join(' · ')

  const { requestDelete: handleDelete, dialog: deleteDialog } = useDeleteTaskConfirm()

  const handleToggleSelect = useCallback(
    (taskId: string, shiftKey: boolean) => {
      onToggleSelect?.(taskId, tasks.map((t) => t.id), shiftKey)
    },
    [onToggleSelect, tasks],
  )

  return (
    <div
      data-column-mode={colMode}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className={`flex flex-col shrink-0 transition-[width,flex-basis]${className ? ` ${className}` : ` ${columnWidthClass(colMode, expandedTransient)}`}`}
    >
      <div className={`flex items-center ${compact ? 'justify-center' : 'justify-between px-3'} py-2 mb-2`}>
        {!compact && (
          <span className="text-xs text-fg-subtle flex items-center gap-1.5 min-w-0" title={groupTitle}>
            <Icon icon={Bot} size="sm" />
            <span className="font-semibold truncate">{groupTitle}</span>
          </span>
        )}
        <span className="text-xs text-fg-subtle bg-surface-sunken rounded-full px-2 py-0.5" data-testid="column-count-badge">{tasks.length}</span>
      </div>

      <div
        ref={setNodeRef}
        className={`flex-1 flex flex-col ${compact ? 'items-center py-2' : 'gap-3 p-2'} rounded-lg min-h-[100px] transition-colors ${isOver && dropState !== 'illegal' ? 'bg-border/50' : 'bg-surface-sunken/30'} ${dropStateClass(dropState)}`}
      >
        {compact && (
          <span title={groupTitle} style={{ writingMode: 'vertical-rl' }} className="text-sm font-semibold text-fg-muted">
            {groupTitle}
          </span>
        )}
        {!compact && tasks.map((task) => (
          <TaskCard
            key={task.id}
            task={task}
            isRunning={runningTaskIds.has(task.id)}
            rateLimitedUntil={rateLimitedTaskIds?.get(task.id)}
            costWarned={costWarnedTaskIds?.has(task.id)}
            onDelete={handleDelete}
            onDuplicate={onDuplicate}
            showColumnLabel={labelDisplayNameByName(task.label, labels)}
            selected={selectedIds?.has(task.id)}
            onToggleSelect={onToggleSelect ? handleToggleSelect : undefined}
          />
        ))}
        {!compact && tasks.length === 0 && (
          <div className="text-center text-fg-subtle text-sm py-8">No tasks</div>
        )}
      </div>
      {deleteDialog}
    </div>
  )
}
