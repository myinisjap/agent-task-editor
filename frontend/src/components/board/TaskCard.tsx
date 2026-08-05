import { memo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useDraggable } from '@dnd-kit/core'
import { CSS } from '@dnd-kit/utilities'
import type { Task } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { useReposStore } from '../../stores/repos'
import GitStateBadge from './GitStateBadge'
import { PRIORITY_LEVELS, priorityLabel, toPriority, type PriorityValue } from '../../lib/priority'
import { blockReasonLabel, isTransientBlockReason } from '../../lib/blockReason'
import { formatRelativeCountdown } from '../../lib/format'

const TYPE_COLORS: Record<string, string> = {
  feature: 'bg-blue-900 text-blue-300',
  bug:     'bg-red-900 text-red-300',
  chore:   'bg-slate-700 text-slate-300',
  spike:   'bg-purple-900 text-purple-300',
}

const TASK_TYPES = ['feature', 'bug', 'chore', 'spike']

function TaskCard({
  task,
  isRunning,
  rateLimitedUntil,
  costWarned,
  onDelete,
  onDuplicate,
  isEditable,
  showColumnLabel,
  selected,
  onToggleSelect,
}: {
  task: Task
  isRunning?: boolean
  rateLimitedUntil?: string
  /** True once this task has crossed the cost early-warning threshold (see task.cost_warning WS event) */
  costWarned?: boolean
  onDelete?: (taskId: string) => void
  /** When set, renders a "Duplicate task" button that opens a pre-filled New Task modal for this task. */
  onDuplicate?: (task: Task) => void
  isEditable?: boolean
  /** When set, renders a muted column-name badge on the card (used in condensed view) */
  showColumnLabel?: string
  /** Multi-select state for bulk actions; checkbox is shown on hover or while selected */
  selected?: boolean
  onToggleSelect?: (taskId: string, shiftKey: boolean) => void
}) {
  const navigate = useNavigate()
  const upsert = useTasksStore((s) => s.upsert)
  const repoName = useReposStore((s) => s.byId(task.repo_id))?.name
  // A task with any unsatisfied blocker is gated from dispatch; mute it and show
  // a badge so an idle-looking card isn't mysterious.
  const blocked = (task.blocked_by_count ?? 0) > 0
  const isChild = !!task.parent_task_id
  const subtaskTotal = task.subtask_total ?? 0
  const subtaskConflicts = task.subtask_conflicts ?? 0
  const [editing, setEditing] = useState(false)
  const [editTitle, setEditTitle] = useState(task.title)
  const [editDesc, setEditDesc] = useState(task.description ?? '')
  const [editType, setEditType] = useState(task.type)
  const [editPriority, setEditPriority] = useState<PriorityValue>(task.priority ?? 0)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')

  // The Pause/Archive/Edit/Duplicate/Delete action cluster is visually
  // hover-only (opacity-0 until :hover) to avoid cluttering the card, but a
  // *static* tabIndex={-1} on those buttons would make them permanently
  // keyboard-unreachable — strictly worse than the original bug (invisible
  // but focusable). Instead, track hover/focus in JS (`cardActive`, set via
  // onMouseEnter/Leave + onFocus/Blur on the card below) and use it to
  // drive both the buttons' opacity *and* their tabIndex (0 when visible,
  // -1 when not), so a keyboard user can Tab onto the card, see the buttons
  // become visible, and continue tabbing into them — CSS-only `:hover`/
  // `:focus-within` can't drive `tabIndex` (a DOM attribute, not a style),
  // hence the JS state. Touch devices (which can't hover) always show them
  // via `isTouchDevice`, matching the existing `no-hover:opacity-100` class.
  const [cardActive, setCardActive] = useState(false)
  const [isTouchDevice] = useState(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return false
    return window.matchMedia('(hover: none)').matches
  })
  const controlsVisible = cardActive || isTouchDevice
  const actionTabIndex = controlsVisible ? 0 : -1

  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: task.id,
    disabled: editing,
  })

  const style = {
    transform: CSS.Translate.toString(transform),
    opacity: isDragging ? 0.4 : 1,
    cursor: editing ? 'default' : isDragging ? 'grabbing' : 'grab',
  }

  const handleEditClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    setEditTitle(task.title)
    setEditDesc(task.description ?? '')
    setEditType(task.type)
    setEditPriority(task.priority ?? 0)
    setSaveError('')
    setEditing(true)
  }

  const handleCancel = (e: React.MouseEvent) => {
    e.stopPropagation()
    setEditing(false)
    setSaveError('')
  }

  const handleSave = async (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!editTitle.trim()) return
    setSaving(true)
    setSaveError('')
    try {
      const updated = await api.tasks.update(task.id, {
        title: editTitle.trim(),
        description: editDesc.trim(),
        type: editType,
        priority: editPriority,
      })
      upsert(updated)
      setEditing(false)
    } catch (err) {
      setSaveError(String(err))
    } finally {
      setSaving(false)
    }
  }

  // While in edit mode, render the edit form inside the card
  if (editing) {
    return (
      <div
        ref={setNodeRef}
        style={{ ...style, cursor: 'default' }}
        className="group bg-slate-800 border border-indigo-500 rounded-lg p-3 transition-colors select-none"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex flex-col gap-2">
          <input
            autoFocus
            value={editTitle}
            onChange={(e) => setEditTitle(e.target.value)}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            placeholder="Task title"
            className="w-full text-sm bg-slate-700 border border-slate-600 rounded px-2 py-1.5 text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-400"
          />
          <textarea
            value={editDesc}
            onChange={(e) => setEditDesc(e.target.value)}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            placeholder="Description (optional)"
            rows={3}
            className="w-full text-xs bg-slate-700 border border-slate-600 rounded px-2 py-1.5 text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-400 resize-none"
          />
          <select
            value={editType}
            onChange={(e) => setEditType(e.target.value as Task['type'])}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            className="w-full text-xs bg-slate-700 border border-slate-600 rounded px-2 py-1.5 text-slate-100 focus:outline-none focus:border-indigo-400"
          >
            {TASK_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
          <select
            value={editPriority}
            onChange={(e) => setEditPriority(toPriority(e.target.value))}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            className="w-full text-xs bg-slate-700 border border-slate-600 rounded px-2 py-1.5 text-slate-100 focus:outline-none focus:border-indigo-400"
          >
            {PRIORITY_LEVELS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>

          {saveError && (
            <p className="text-xs text-red-400">{saveError}</p>
          )}

          <div className="flex gap-2 justify-end mt-1">
            <button
              onClick={handleCancel}
              onPointerDown={(e) => e.stopPropagation()}
              disabled={saving}
              className="px-3 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-300 disabled:opacity-50 transition-colors"
            >
              Cancel
            </button>
            <button
              onClick={handleSave}
              onPointerDown={(e) => e.stopPropagation()}
              disabled={saving || !editTitle.trim()}
              className="px-3 py-1 text-xs rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </div>
    )
  }

  // dnd-kit's `attributes` includes `role="button"` + `tabIndex=0` (its
  // `defaultRole`), intended for a single focusable/interactive drag
  // surface. The card renders several real <button>s inside it (Pause/
  // Archive/Edit/Duplicate/Delete below), so applying `role="button"` to the
  // card would nest interactive controls inside an interactive ancestor —
  // invalid ARIA that screen readers flatten (see issue #350). Drop dnd-kit's
  // role/aria-pressed/roledescription from the card (destructured out below)
  // but keep `tabIndex` and the drag *listeners* (pointer/touch/keyboard
  // activation) so the card stays keyboard-focusable, draggable, and
  // Enter-openable — it's just no longer announced as a "button" itself,
  // which is accurate: it's a generic container whose real interactive
  // children (the action buttons) are its accessible controls, plus a
  // click-to-navigate affordance exposed via onClick/onKeyDown+aria-label.
  const { role: _dndRole, 'aria-pressed': _ariaPressed, 'aria-roledescription': _ariaRoleDescription, ...cardAttributes } = attributes

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...listeners}
      {...cardAttributes}
      aria-label={`${task.title} — ${task.label}`}
      onClick={(e) => {
        if (!isDragging) navigate(`/tasks/${task.id}`)
        e.stopPropagation()
      }}
      onKeyDown={(e) => {
        // Enter opens the task; Space is reserved for dnd-kit's
        // pick-up/drop (see TaskBoard's KeyboardSensor keyboardCodes, which
        // restricts drag start/end to Space so the two keys don't collide).
        // Guard on `e.target === e.currentTarget`: this handler is on the
        // card, but React's synthetic onKeyDown bubbles from whichever
        // descendant is actually focused — now that the action buttons are
        // keyboard-reachable (tabIndex 0 while visible, see `cardActive`
        // above), pressing Enter on e.g. the focused Delete button must
        // trigger *that button's* own click, not hijack the keypress to
        // navigate away instead (which previously masked this bug, since
        // the buttons were never reachable to begin with — see issue #350).
        if (e.key === 'Enter' && e.target === e.currentTarget) {
          e.preventDefault()
          navigate(`/tasks/${task.id}`)
        }
      }}
      onMouseEnter={() => setCardActive(true)}
      onMouseLeave={() => setCardActive(false)}
      onFocus={() => setCardActive(true)}
      onBlur={(e) => {
        // React's onBlur fires (via the underlying focusout) for every
        // focus change within the card, including moving focus from the
        // card itself onto one of its own action buttons — only treat this
        // as "left the card" once focus actually moves outside it, or the
        // buttons would flicker `tabIndex={-1}` right as the user tries to
        // Tab into them.
        if (!e.currentTarget.contains(e.relatedTarget)) setCardActive(false)
      }}
      className={`group bg-slate-800 border rounded-lg p-3 hover:border-slate-500 transition-colors select-none ${
        selected ? 'border-indigo-500' : blocked ? 'border-amber-700/60' : 'border-slate-700'
      } ${task.archived || blocked ? 'opacity-60' : ''}`}
    >
      <div className="flex items-start justify-between gap-2 mb-2">
        <div className="flex items-start gap-2 min-w-0">
          {onToggleSelect && (
            <input
              type="checkbox"
              checked={!!selected}
              onChange={() => {}}
              onClick={(e) => {
                e.stopPropagation()
                onToggleSelect(task.id, e.shiftKey)
              }}
              onPointerDown={(e) => e.stopPropagation()}
              className={`mt-0.5 shrink-0 accent-indigo-500 cursor-pointer transition-opacity ${
                selected ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 no-hover:opacity-100'
              }`}
              title="Select for bulk actions"
            />
          )}
          <span className="text-sm text-slate-100 font-medium leading-snug">{task.title}</span>
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
          {isChild && (
            <button
              onClick={(e) => { e.stopPropagation(); navigate(`/tasks/${task.parent_task_id}`) }}
              onPointerDown={(e) => e.stopPropagation()}
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-indigo-900/60 text-indigo-300 font-semibold hover:bg-indigo-800"
              title="Subtask — click to open its parent"
            >
              ↳ subtask
            </button>
          )}
          {isChild && task.merge_status === 'merge_conflict' && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-red-900/70 text-red-300 font-semibold"
              title="Merge-back into the parent's branch conflicted — the parent's agent will resolve it"
            >
              ⚠ conflict
            </span>
          )}
          {isChild && task.merge_status === 'merged' && (
            <span className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-emerald-900/60 text-emerald-300 font-semibold" title="Merged back into the parent's branch">
              ✓ merged
            </span>
          )}
          {subtaskTotal > 0 && (
            <span
              className={`inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded font-semibold ${subtaskConflicts > 0 ? 'bg-red-900/60 text-red-300' : 'bg-slate-700 text-slate-300'}`}
              title={`${task.subtask_done ?? 0} of ${subtaskTotal} subtasks done${subtaskConflicts > 0 ? `, ${subtaskConflicts} in conflict` : ''}`}
            >
              ⑃ {task.subtask_done ?? 0}/{subtaskTotal}{subtaskConflicts > 0 ? ' ⚠' : ''}
            </span>
          )}
          {blocked && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-amber-900/60 text-amber-300 font-semibold"
              title={`Waiting on ${task.blocked_by_count} unfinished dependency${(task.blocked_by_count ?? 0) === 1 ? '' : ' tasks'} — not dispatched until they finish`}
            >
              🔒 Blocked by {task.blocked_by_count}
            </span>
          )}
          {!!task.priority && (
            <span
              className={`text-[10px] px-1.5 py-0.5 rounded font-semibold ${
                task.priority >= 2
                  ? 'bg-red-900/70 text-red-300'
                  : task.priority === 1
                    ? 'bg-orange-900/60 text-orange-300'
                    : 'bg-slate-700 text-slate-400'
              }`}
              title={`Dispatch priority: ${priorityLabel(task.priority)}`}
            >
              {priorityLabel(task.priority)}
            </span>
          )}
          {task.block_reason ? (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-red-900/60 text-red-300 font-semibold"
              title={
                task.block_reason.message +
                (task.block_reason.clears_at && isTransientBlockReason(task.block_reason)
                  ? ` — clears ${formatRelativeCountdown(task.block_reason.clears_at)}`
                  : '')
              }
            >
              🚫 {blockReasonLabel(task.block_reason)}
              {task.block_reason.clears_at && isTransientBlockReason(task.block_reason)
                ? ` (${formatRelativeCountdown(task.block_reason.clears_at)})`
                : ''}
            </span>
          ) : (
            task.queue_position != null && (
              <span
                className="text-[10px] px-1.5 py-0.5 rounded bg-slate-700 text-slate-400 font-medium"
                title="Position in the current agent-pickup queue (priority order), waiting for a free worker"
              >
                #{task.queue_position + 1} in queue
              </span>
            )
          )}
          {task.archived && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-slate-700 text-slate-400 font-semibold"
              title="Task is archived — hidden from the default board view"
            >
              🗄 Archived
            </span>
          )}
          {task.paused && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-amber-900/70 text-amber-300 font-semibold"
              title="Task is paused — will not be picked up by an agent"
            >
              ⏸ Paused
            </span>
          )}
          {isRunning && (
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse mt-1" title="Agent running" />
          )}
          {rateLimitedUntil && !isRunning && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-amber-900/60 text-amber-300 font-medium"
              title={`Rate limited by API. Retrying after ${new Date(rateLimitedUntil).toLocaleTimeString()}`}
            >
              ⏸ API limit
            </span>
          )}
          {costWarned && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-amber-900/60 text-amber-300 font-medium"
              title="Cumulative cost has crossed the early-warning threshold of its budget. See GET/PUT /settings/cost-warning for the threshold."
            >
              💰 Budget warning
            </span>
          )}
          {task.next_retry_at && !isRunning && (
            <span
              className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-amber-900/60 text-amber-300 font-medium"
              title={`Auto-retrying after a transient error (attempt ${task.transient_retry_count ?? '?'}). Next attempt ~${new Date(task.next_retry_at).toLocaleTimeString()}`}
            >
              ↻ Retrying ({task.transient_retry_count ?? '?'})
            </span>
          )}
          <button
            onClick={async (e) => {
              e.stopPropagation()
              try {
                const updated = await api.tasks.setPaused(task.id, !task.paused)
                upsert(updated)
              } catch (err) {
                alert(String(err))
              }
            }}
            onPointerDown={(e) => e.stopPropagation()}
            // Visually hidden until the card is hovered/focused or the
            // device can't hover (touch) — see `controlsVisible` above.
            // tabIndex tracks the same condition dynamically (0 when
            // visible, -1 when not) rather than a permanent -1, so the
            // button stays reachable via keyboard once the card is focused,
            // instead of being unreachable outright (see issue #350).
            tabIndex={actionTabIndex}
            className={`${controlsVisible ? 'opacity-100' : 'opacity-0'} group-hover:opacity-100 no-hover:opacity-100 text-slate-500 hover:text-amber-400 transition-opacity leading-none`}
            title={task.paused ? 'Resume task' : 'Pause task'}
          >
            {task.paused ? '▶' : '⏸'}
          </button>
          <button
            onClick={async (e) => {
              e.stopPropagation()
              try {
                const updated = await api.tasks.setArchived(task.id, !task.archived)
                upsert(updated)
              } catch (err) {
                alert(String(err))
              }
            }}
            onPointerDown={(e) => e.stopPropagation()}
            tabIndex={actionTabIndex}
            className={`${controlsVisible ? 'opacity-100' : 'opacity-0'} group-hover:opacity-100 no-hover:opacity-100 text-slate-500 hover:text-indigo-400 transition-opacity leading-none`}
            title={task.archived ? 'Unarchive task' : 'Archive task — hide from the board'}
          >
            {task.archived ? '↩' : '🗄'}
          </button>
          {isEditable && (
            <button
              onClick={handleEditClick}
              onPointerDown={(e) => e.stopPropagation()}
              tabIndex={actionTabIndex}
              className={`${controlsVisible ? 'opacity-100' : 'opacity-0'} group-hover:opacity-100 no-hover:opacity-100 text-slate-500 hover:text-indigo-400 transition-opacity leading-none`}
              title="Edit task"
            >
              ✎
            </button>
          )}
          {onDuplicate && (
            <button
              onClick={(e) => {
                e.stopPropagation()
                onDuplicate(task)
              }}
              onPointerDown={(e) => e.stopPropagation()}
              tabIndex={actionTabIndex}
              className={`${controlsVisible ? 'opacity-100' : 'opacity-0'} group-hover:opacity-100 no-hover:opacity-100 text-slate-500 hover:text-indigo-400 transition-opacity leading-none`}
              title="Duplicate task"
            >
              ⧉
            </button>
          )}
          {onDelete && (
            <button
              onClick={(e) => {
                e.stopPropagation()
                if (window.confirm('Delete this task?')) onDelete(task.id)
              }}
              onPointerDown={(e) => e.stopPropagation()}
              tabIndex={actionTabIndex}
              className={`${controlsVisible ? 'opacity-100' : 'opacity-0'} group-hover:opacity-100 no-hover:opacity-100 text-slate-500 hover:text-red-400 transition-opacity leading-none`}
              title="Delete task"
            >
              ✕
            </button>
          )}
        </div>
      </div>
      {showColumnLabel && (
        <div className="mb-1.5">
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-700 text-slate-400 font-medium tracking-wide">
            {showColumnLabel}
          </span>
        </div>
      )}
      <div className="flex items-center gap-2">
        <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${TYPE_COLORS[task.type] ?? TYPE_COLORS.feature}`}>
          {task.type}
        </span>
        <span className="text-xs text-slate-500 truncate">{task.id.slice(0, 8)}</span>
        <GitStateBadge branch={task.branch} gitState={task.git_state} prMergeable={task.pr_mergeable} />
        {repoName && (
          <span className="text-xs text-slate-400 truncate max-w-[80px] ml-auto" title={repoName}>
            {repoName}
          </span>
        )}
      </div>
    </div>
  )
}

// Board renders can involve hundreds of cards; memoize so an unrelated
// tasks-store change (a different task's upsert) doesn't re-run useDraggable
// / useReposStore for every card. Effective when zustand's upsert() replaces
// only the changed task's array entry, leaving unaffected tasks' object
// identity intact — see TaskColumn/TaskBoard for the narrowed selectors that
// make this worthwhile.
export default memo(TaskCard)
