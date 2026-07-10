import { useState } from 'react'
import type { TaskLabelHistoryEntry } from '../../api/client'

// formatTimestamp renders a compact, locale-aware date/time for a history entry.
function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

// triggerBadgeClass returns the badge color for a transition's trigger type,
// matching the semantic colors used elsewhere for run status (agent = pink
// for waiting-on-agent-like activity, human = emerald for a completed human
// action, subtasks_complete = slate as a neutral system trigger).
function triggerBadgeClass(trigger: string): string {
  switch (trigger) {
    case 'agent':
      return 'bg-purple-900/60 text-purple-200'
    case 'human':
      return 'bg-emerald-900/60 text-emerald-200'
    case 'subtasks_complete':
      return 'bg-slate-700 text-slate-300'
    default:
      return 'bg-slate-700 text-slate-300'
  }
}

function NotePanel({ note }: { note: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-slate-700 pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300 w-full text-left py-0.5"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span>note</span>
      </button>
      {open && (
        <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {note}
        </pre>
      )}
    </div>
  )
}

export default function LabelHistoryList({ history }: { history: TaskLabelHistoryEntry[] }) {
  if (history.length === 0) return null

  return (
    <div>
      <p className="text-xs text-slate-500 mb-2">Label history</p>
      <div className="flex flex-col gap-1">
        {history.map((entry) => (
          <div key={entry.id} className="text-xs px-2 py-1.5 rounded text-slate-400">
            <div className="flex items-center justify-between gap-2">
              <span className="truncate">
                {entry.from_label ? (
                  <>
                    <span className="font-mono">{entry.from_label}</span>
                    <span className="mx-1 text-slate-600">→</span>
                  </>
                ) : null}
                <span className="font-mono text-slate-200">{entry.to_label}</span>
              </span>
              <span className={`shrink-0 px-1.5 py-0.5 rounded text-[11px] ${triggerBadgeClass(entry.trigger)}`}>
                {entry.trigger}
              </span>
            </div>
            <div className="text-slate-500 text-[11px] mt-0.5 flex items-center gap-1">
              <span>{formatTimestamp(entry.created_at)}</span>
              {entry.trigger === 'human' && (
                <span>· by {entry.actor_id ? entry.actor_id : 'unknown'}</span>
              )}
              {entry.trigger !== 'human' && entry.actor_id && (
                <span className="font-mono">· {entry.actor_id.slice(0, 8)}</span>
              )}
            </div>
            {entry.note && <NotePanel note={entry.note} />}
          </div>
        ))}
      </div>
    </div>
  )
}
