import AgentLogEntry from '../board/AgentLogEntry'
import type { StepGroup, StepKind } from '../../lib/runTimeline'

const KIND_ICON: Record<StepKind, string> = {
  read: '📄',
  edit: '✏️',
  command: '⚡',
  test: '🧪',
  message: '💬',
  human: '🙋',
  error: '✕',
  system: '•',
  tool: '🔧',
}

// Single-row groups of these kinds are shown directly: hiding them is pointless.
const ALWAYS_OPEN = new Set<StepKind>(['message', 'human', 'error'])

function isAlwaysOpen(group: StepGroup): boolean {
  return ALWAYS_OPEN.has(group.kind) && group.rows.length === 1
}

// One collapsible step group. Open/closed state is owned by the parent
// (RunLogPane) because virtualization unmounts off-screen groups.
export default function RunStepGroup({ group, open, onToggle, failed }: {
  group: StepGroup
  open: boolean
  onToggle: () => void
  /** A command or test in the group failed (shown as a red marker). */
  failed: boolean
}) {
  if (isAlwaysOpen(group)) {
    return (
      <div className="my-0.5" data-testid="step-group" data-kind={group.kind}>
        <AgentLogEntry row={group.rows[0]} />
      </div>
    )
  }
  const isErr = group.hasError
  return (
    <div className="my-0.5" data-testid="step-group" data-kind={group.kind}>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className={`w-full flex items-center gap-2 py-1.5 px-3 rounded-lg text-left border text-xs transition-colors ${
          isErr
            ? 'bg-red-950/20 border-red-800/30 text-red-300 hover:bg-red-950/40'
            : 'bg-slate-900/60 border-slate-800 text-slate-300 hover:bg-slate-800/60'
        }`}
      >
        <span className="shrink-0" aria-hidden="true">{KIND_ICON[group.kind]}</span>
        <span className="flex-1 min-w-0 truncate" title={group.summary}>{group.summary}</span>
        {group.pending && (
          <span className="text-yellow-300/90 bg-yellow-500/10 border border-yellow-500/30 rounded-full px-1.5 text-[10px] animate-pulse">
            running
          </span>
        )}
        {failed && <span className="text-red-400 shrink-0" aria-label="failed" title="Something in this group failed">✕</span>}
        <span className="text-slate-500 shrink-0" aria-hidden="true">{open ? '▲' : '▼'}</span>
      </button>
      {open && (
        <div className="ml-3 mt-0.5 border-l border-slate-800 pl-1">
          {group.rows.map((row) => <AgentLogEntry key={row.key} row={row} />)}
        </div>
      )}
    </div>
  )
}
