import { useNavigate } from 'react-router-dom'
import { useDraggable } from '@dnd-kit/core'
import { CSS } from '@dnd-kit/utilities'
import type { Task } from '../../api/client'

const TYPE_COLORS: Record<string, string> = {
  feature: 'bg-blue-900 text-blue-300',
  bug:     'bg-red-900 text-red-300',
  chore:   'bg-slate-700 text-slate-300',
  spike:   'bg-purple-900 text-purple-300',
}

export default function TaskCard({ task, isRunning }: { task: Task; isRunning?: boolean }) {
  const navigate = useNavigate()
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({ id: task.id })

  const style = {
    transform: CSS.Translate.toString(transform),
    opacity: isDragging ? 0.4 : 1,
    cursor: isDragging ? 'grabbing' : 'grab',
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
      className="bg-slate-800 border border-slate-700 rounded-lg p-3 hover:border-slate-500 transition-colors select-none"
    >
      <div className="flex items-start justify-between gap-2 mb-2">
        <span className="text-sm text-slate-100 font-medium leading-snug">{task.title}</span>
        {isRunning && (
          <span className="shrink-0 w-2 h-2 rounded-full bg-emerald-400 animate-pulse mt-1" title="Agent running" />
        )}
      </div>
      <div className="flex items-center gap-2">
        <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${TYPE_COLORS[task.type] ?? TYPE_COLORS.feature}`}>
          {task.type}
        </span>
        <span className="text-xs text-slate-500 truncate">{task.id.slice(0, 8)}</span>
      </div>
    </div>
  )
}
