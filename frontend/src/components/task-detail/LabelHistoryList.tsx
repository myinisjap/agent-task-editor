import { ChevronDown, ChevronRight } from 'lucide-react'
import Icon from '../ui/Icon'
import { labelDisplayNameByName } from '../../lib/labelDisplay'
import { useState } from 'react'
import type { TaskLabelHistoryEntry, WorkflowLabel } from '../../api/client'

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
const isPolicy = (e: TaskLabelHistoryEntry) => e.actor_id === 'policy'

function triggerBadgeClass(trigger: string): string {
  switch (trigger) {
    case 'agent':
      return 'bg-purple-900/60 text-purple-200'
    case 'human':
      return 'bg-emerald-900/60 text-emerald-200'
    case 'subtasks_complete':
      return 'bg-border text-fg-muted'
    default:
      return 'bg-border text-fg-muted'
  }
}

function NotePanel({ note }: { note: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-border pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-fg-subtle hover:text-fg-muted w-full text-left py-0.5"
      >
        <Icon icon={open ? ChevronDown : ChevronRight} size="sm" />
        <span>note</span>
      </button>
      {open && (
        <pre className="text-xs text-fg-muted bg-surface-sunken rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {note}
        </pre>
      )}
    </div>
  )
}

// Human-triggered entries show the named API token's actor, or "you" when no
// named tokens (API_TOKENS) are configured and the actor is empty.
export default function LabelHistoryList({
  history,
  labels,
  onRemember,
  onShouldHaveAsked,
}: {
  history: TaskLabelHistoryEntry[]
  labels?: WorkflowLabel[]
  onRemember?: (text: string) => void
  onShouldHaveAsked?: () => void
}) {
  if (history.length === 0) return null

  return (
    <div>
      <p className="text-xs text-fg-subtle mb-2">Label history</p>
      <div className="flex flex-col gap-1">
        {history.map((entry) => (
          <div key={entry.id} className="text-xs px-2 py-1.5 rounded text-fg-muted">
            <div className="flex items-center justify-between gap-2">
              <span className="truncate">
                {entry.from_label ? (
                  <>
                    <span>{labelDisplayNameByName(entry.from_label, labels)}</span>
                    <span className="mx-1 text-fg-subtle">→</span>
                  </>
                ) : null}
                <span className="text-fg">{labelDisplayNameByName(entry.to_label, labels)}</span>
              </span>
              <span className={`shrink-0 px-1.5 py-0.5 rounded text-[11px] ${isPolicy(entry) ? 'bg-teal-900/60 text-teal-200' : triggerBadgeClass(entry.trigger)}`}>
                {isPolicy(entry) ? 'Approved by policy' : entry.trigger}
              </span>
            </div>
            <div className="text-fg-subtle text-[11px] mt-0.5 flex items-center gap-1">
              <span>{formatTimestamp(entry.created_at)}</span>
              {entry.trigger === 'human' && (
                <span>· {isPolicy(entry) ? 'by policy' : `by ${entry.actor_id ? entry.actor_id : 'you'}`}</span>
              )}
              {entry.trigger !== 'human' && entry.actor_id && (
                <span className="font-mono">· {entry.actor_id.slice(0, 8)}</span>
              )}
            </div>
            {entry.note && (isPolicy(entry)
              ? <p data-testid="policy-note" className="ml-2 mt-0.5 text-[11px] text-fg-muted border-l border-border pl-2">{entry.note}</p>
              : <NotePanel note={entry.note} />)}
            {onShouldHaveAsked && isPolicy(entry) && (
              <button
                type="button"
                onClick={onShouldHaveAsked}
                className="ml-2 mt-0.5 text-[11px] text-accent-fg hover:underline"
              >
                Should have asked
              </button>
            )}
            {onRemember && entry.trigger === 'human' && entry.actor_id !== 'policy' && entry.note && (
              <button
                onClick={() => onRemember(entry.note ?? '')}
                aria-label="Remember for this repo"
                className="ml-2 mt-0.5 text-[11px] text-blue-400 hover:text-blue-300 hover:underline"
              >
                Remember for this repo
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
