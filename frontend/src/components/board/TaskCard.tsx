import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useDraggable } from '@dnd-kit/core'
import { CSS } from '@dnd-kit/utilities'
import type { Task } from '../../api/client'
import { api } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { useReposStore } from '../../stores/repos'
import GitStateBadge from './GitStateBadge'

const TYPE_COLORS: Record<string, string> = {
  feature: 'bg-blue-900 text-blue-300',
  bug:     'bg-red-900 text-red-300',
  chore:   'bg-slate-700 text-slate-300',
  spike:   'bg-purple-900 text-purple-300',
}

const TASK_TYPES = ['feature', 'bug', 'chore', 'spike']

export default function TaskCard({
  task,
  isRunning,
  rateLimitedUntil,
  onDelete,
  isEditable,
}: {
  task: Task
  isRunning?: boolean
  rateLimitedUntil?: string
  onDelete?: () => void
  isEditable?: boolean
}) {
  const navigate = useNavigate()
  const { upsert } = useTasksStore()
  const repoName = useReposStore((s) => s.byId(task.repo_id))?.name
  const [isExpanded, setIsExpanded] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editTitle, setEditTitle] = useState(task.title)
  const [editDesc, setEditDesc] = useState(task.description ?? '')
  const [editType, setEditType] = useState(task.type)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')

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
            onChange={(e) => setEditType(e.target.value)}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            className="w-full text-xs bg-slate-700 border border-slate-600 rounded px-2 py-1.5 text-slate-100 focus:outline-none focus:border-indigo-400"
          >
            {TASK_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
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

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...listeners}
      {...attributes}
      onClick={(e) => {
        if (!isDragging) navigate(`/tasks/${task.id}`)
        e.stopPropagation()
      }}
      className="group bg-slate-800 border border-slate-700 rounded-lg p-3 hover:border-slate-500 transition-colors select-none"
    >
      <div className="flex items-start justify-between gap-2 mb-2">
        <span className="text-sm text-slate-100 font-medium leading-snug">{task.title}</span>
        <div className="flex items-center gap-1.5 shrink-0">
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
          {isEditable && (
            <button
              onClick={handleEditClick}
              onPointerDown={(e) => e.stopPropagation()}
              className="opacity-0 group-hover:opacity-100 text-slate-500 hover:text-indigo-400 transition-opacity leading-none"
              title="Edit task"
            >
              ✎
            </button>
          )}
          {onDelete && (
            <button
              onClick={(e) => {
                e.stopPropagation()
                if (window.confirm('Delete this task?')) onDelete()
              }}
              onPointerDown={(e) => e.stopPropagation()}
              className="opacity-0 group-hover:opacity-100 text-slate-500 hover:text-red-400 transition-opacity leading-none"
              title="Delete task"
            >
              ✕
            </button>
          )}
        </div>
      </div>
      <div className="flex items-center gap-2">
        <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${TYPE_COLORS[task.type] ?? TYPE_COLORS.feature}`}>
          {task.type}
        </span>
        <span className="text-xs text-slate-500 truncate">{task.id.slice(0, 8)}</span>
        <GitStateBadge branch={task.branch} gitState={task.git_state} />
        {repoName && (
          <span className="text-xs text-slate-400 truncate max-w-[80px] ml-auto" title={repoName}>
            {repoName}
          </span>
        )}
      </div>

      {isExpanded && task.agent_notes && (
        <div className="mt-3 pt-3 border-t border-slate-700">
          <span className="text-[10px] uppercase tracking-wider text-slate-500 font-bold">Agent Notes</span>
          <p className="text-xs text-slate-300 mt-1 whitespace-pre-wrap">{task.agent_notes}</p>
        </div>
      )}

      <button
        onClick={(e) => {
          e.stopPropagation()
          setIsExpanded(!isExpanded)
        }}
        className="mt-2 text-[10px] text-slate-500 hover:text-slate-300 transition-colors"
      >
        {isExpanded ? 'Hide Notes' : 'Show Notes'}
      </button>
    </div>
  )
}
