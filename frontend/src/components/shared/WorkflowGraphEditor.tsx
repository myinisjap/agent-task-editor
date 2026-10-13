import { useState } from 'react'
import {
  DEFAULT_LABEL_COLOR,
  TRANSITION_KINDS,
  addLabel,
  addTransition,
  deleteLabel,
  isGated,
  kindOf,
  removeTransition,
  renameLabel,
  setGate,
  setTransitionKind,
  transitionsTouching,
  updateLabel,
  type DraftLabel,
  type TransitionKind,
  type VerifyMode,
  type WorkflowDraft,
} from '../../lib/workflowEdit'

type Props = {
  draft: WorkflowDraft
  onChange: (next: WorkflowDraft) => void
  selectedLabel: string | null
  onSelectLabel: (name: string | null) => void
  /** Tasks currently on each label as saved on the server; labels with tasks cannot be renamed or deleted. */
  taskCounts: Map<string, number>
  /** Names of labels that exist on the server (renames/deletes of these are checked against taskCounts). */
  savedLabelNames: Set<string>
  /** Task counts are still loading: saved labels stay locked until they arrive. */
  tasksPending?: boolean
  /** Bump to prefill the connect form (set by dragging on the graph). */
  connectPrefill?: { from: string; to: string; n: number } | null
}

const field = 'bg-slate-900 border border-slate-600 rounded px-2 py-1 text-sm text-slate-200'
const btn = 'px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-100 disabled:opacity-50'

function LabelForm({ draft, label, onChange, onSelectLabel, taskCount, locked, pending }: {
  draft: WorkflowDraft
  label: DraftLabel
  onChange: (d: WorkflowDraft) => void
  onSelectLabel: (n: string | null) => void
  taskCount: number
  locked: boolean
  pending: boolean
}) {
  const [name, setName] = useState(label.name)
  const [error, setError] = useState<string | null>(null)
  const patch = (p: Partial<Omit<DraftLabel, 'name'>>) => onChange(updateLabel(draft, label.name, p))
  const outgoing = draft.transitions.some((t) => t.from_label === label.name)
  const touching = transitionsTouching(draft, label.name)

  const rename = () => {
    const r = renameLabel(draft, label.name, name)
    if (r.error) { setError(r.error); return }
    setError(null)
    onChange(r.draft)
    onSelectLabel(name.trim())
  }

  return (
    <section aria-label={`Edit label ${label.name}`} className="space-y-2 border-t border-slate-700 pt-3">
      <h3 className="text-sm font-semibold text-slate-100">Label: {label.name}</h3>
      {locked && (
        <p className="text-xs text-amber-300">
          {pending ? 'Checking which tasks are on this label…' : <>{taskCount} task{taskCount === 1 ? '' : 's'} on this label: it cannot be renamed or deleted until they are moved.</>}
        </p>
      )}
      <div className="flex gap-1">
        <input aria-label="Label name" value={name} onChange={(e) => setName(e.target.value)} disabled={locked} className={`${field} flex-1 min-w-0`} />
        <button type="button" className={btn} disabled={locked || name.trim() === label.name} onClick={rename}>Rename</button>
      </div>
      {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
      <div className="flex items-center gap-2 text-sm text-slate-200">
        <label htmlFor="edit-label-color">Color</label>
        <input
          id="edit-label-color"
          type="color"
          value={/^#[0-9a-fA-F]{6}$/.test(label.color) ? label.color : DEFAULT_LABEL_COLOR}
          onChange={(e) => patch({ color: e.target.value })}
        />
        <input aria-label="Color hex" value={label.color} onChange={(e) => patch({ color: e.target.value })} className={`${field} w-24`} />
      </div>
      <label className="flex items-center gap-2 text-sm text-slate-200">
        <input type="checkbox" checked={!label.agent_ignore} onChange={(e) => patch({ agent_ignore: !e.target.checked })} />
        Agents work this label
      </label>
      <label className="flex items-center gap-2 text-sm text-slate-200">
        <input type="checkbox" checked={isGated(draft, label.name)} disabled={!outgoing}
          onChange={(e) => onChange(setGate(draft, label.name, e.target.checked))} />
        Human approval gate on outgoing transitions
      </label>
      <label className="flex items-center gap-2 text-sm text-slate-200">
        <input type="checkbox" checked={label.is_terminal} onChange={(e) => patch({ is_terminal: e.target.checked })} />
        Terminal (done)
      </label>
      <label className="flex items-center gap-2 text-sm text-slate-200">
        <input type="checkbox" checked={label.create_pr} onChange={(e) => patch({ create_pr: e.target.checked })} />
        Open a PR on entry
      </label>
      <div className="flex items-center gap-2 text-sm text-slate-200">
        <label htmlFor="edit-wip">WIP limit</label>
        <input
          id="edit-wip"
          type="number"
          min={1}
          placeholder="none"
          value={label.wip_limit ?? ''}
          onChange={(e) => patch({ wip_limit: e.target.value === '' ? null : Number(e.target.value) })}
          className={`${field} w-20`}
        />
        <label className="flex items-center gap-1 text-xs">
          <input type="checkbox" checked={label.wip_limit_hard} disabled={label.wip_limit === null}
            onChange={(e) => patch({ wip_limit_hard: e.target.checked })} />
          hard
        </label>
      </div>
      <div className="flex items-center gap-2 text-sm text-slate-200">
        <label htmlFor="edit-verify">Verify</label>
        <select id="edit-verify" value={label.verify_mode} onChange={(e) => patch({ verify_mode: e.target.value as VerifyMode })} className={field}>
          <option value="inherit">Inherit repo command</option>
          <option value="custom">Custom command</option>
          <option value="disabled">Disabled</option>
        </select>
      </div>
      {label.verify_mode === 'custom' && (
        <input aria-label="Verify command" placeholder="e.g. npm test" value={label.verify_command}
          onChange={(e) => patch({ verify_command: e.target.value })} className={`${field} w-full`} />
      )}
      <button
        type="button"
        disabled={locked}
        className="px-2 py-1 text-xs rounded bg-red-900/60 hover:bg-red-800 text-red-100 disabled:opacity-50"
        onClick={() => { onChange(deleteLabel(draft, label.name)); onSelectLabel(null) }}
      >
        Delete label{touching > 0 ? ` (and ${touching} transition${touching === 1 ? '' : 's'})` : ''}
      </button>
    </section>
  )
}

/** Side panel for editing a workflow draft: add labels, connect them, and tune the selected label. */
export default function WorkflowGraphEditor({ draft, onChange, selectedLabel, onSelectLabel, taskCounts, savedLabelNames, tasksPending, connectPrefill }: Props) {
  const [newName, setNewName] = useState('')
  const [newColor, setNewColor] = useState(DEFAULT_LABEL_COLOR)
  const [addError, setAddError] = useState<string | null>(null)
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [kind, setKind] = useState<TransitionKind>('agent_success')
  const [connError, setConnError] = useState<string | null>(null)
  const [seenPrefill, setSeenPrefill] = useState<number | null>(null)

  // Dragging on the graph prefills the form (adjust-state-during-render pattern).
  if (connectPrefill && connectPrefill.n !== seenPrefill) {
    setSeenPrefill(connectPrefill.n)
    setFrom(connectPrefill.from)
    setTo(connectPrefill.to)
  }

  const names = draft.labels.map((l) => l.name)
  const selected = draft.labels.find((l) => l.name === selectedLabel)
  const countFor = (n: string) => (savedLabelNames.has(n) ? taskCounts.get(n) ?? 0 : 0)

  const add = (e: React.FormEvent) => {
    e.preventDefault()
    const r = addLabel(draft, newName, newColor)
    if (r.error) { setAddError(r.error); return }
    setAddError(null)
    onChange(r.draft)
    onSelectLabel(newName.trim())
    setNewName('')
  }

  const connect = (e: React.FormEvent) => {
    e.preventDefault()
    const r = addTransition(draft, from, to, kind)
    if (r.error) { setConnError(r.error); return }
    setConnError(null)
    onChange(r.draft)
  }

  return (
    <aside aria-label="Edit workflow" className="md:w-80 shrink-0 rounded border border-slate-700 bg-slate-900 p-3 space-y-3 overflow-auto">
      <form onSubmit={add} className="space-y-1" aria-label="Add label">
        <h2 className="text-sm font-semibold text-slate-100">Add label</h2>
        <div className="flex gap-1">
          <input aria-label="New label name" placeholder="name" value={newName} onChange={(e) => setNewName(e.target.value)} className={`${field} flex-1 min-w-0`} />
          <input aria-label="New label color" type="color" value={newColor} onChange={(e) => setNewColor(e.target.value)} />
          <button type="submit" className={btn}>Add label</button>
        </div>
        {addError && <p role="alert" className="text-xs text-red-300">{addError}</p>}
      </form>

      <form onSubmit={connect} className="space-y-1 border-t border-slate-700 pt-3" aria-label="Connect labels">
        <h2 className="text-sm font-semibold text-slate-100">Connect labels</h2>
        <p className="text-xs text-slate-400">Drag from a stage's bottom dot to another's top dot, or pick them here.</p>
        <div className="flex flex-wrap gap-1">
          <select aria-label="Transition from" value={from} onChange={(e) => setFrom(e.target.value)} className={`${field} flex-1 min-w-0`}>
            <option value="">From…</option>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
          <select aria-label="Transition to" value={to} onChange={(e) => setTo(e.target.value)} className={`${field} flex-1 min-w-0`}>
            <option value="">To…</option>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </div>
        <div className="flex gap-1">
          <select aria-label="Transition type" value={kind} onChange={(e) => setKind(e.target.value as TransitionKind)} className={`${field} flex-1 min-w-0`}>
            {TRANSITION_KINDS.map((k) => <option key={k.value} value={k.value}>{k.label}</option>)}
          </select>
          <button type="submit" className={btn}>Add transition</button>
        </div>
        {connError && <p role="alert" className="text-xs text-red-300">{connError}</p>}
      </form>

      <section aria-label="Transitions" className="space-y-1 border-t border-slate-700 pt-3">
        <h2 className="text-sm font-semibold text-slate-100">Transitions</h2>
        {draft.transitions.length === 0 && <p className="text-xs text-slate-400">None yet.</p>}
        <ul className="space-y-1">
          {draft.transitions.map((t, i) => (
            <li key={i} className="flex items-center gap-1 text-xs text-slate-200">
              <span className="flex-1 min-w-0 truncate" title={`${t.from_label} → ${t.to_label}`}>{t.from_label} → {t.to_label}</span>
              <select
                aria-label={`Type of ${t.from_label} to ${t.to_label}`}
                value={kindOf(t)}
                onChange={(e) => onChange(setTransitionKind(draft, i, e.target.value as TransitionKind))}
                className={`${field} max-w-[9rem]`}
              >
                {TRANSITION_KINDS.map((k) => <option key={k.value} value={k.value}>{k.label}</option>)}
              </select>
              <button type="button" className={btn} aria-label={`Delete transition ${t.from_label} to ${t.to_label} (${kindOf(t)})`}
                onClick={() => onChange(removeTransition(draft, i))}>×</button>
            </li>
          ))}
        </ul>
      </section>

      {selected ? (
        <LabelForm
          key={selected.name}
          draft={draft}
          label={selected}
          onChange={onChange}
          onSelectLabel={onSelectLabel}
          taskCount={countFor(selected.name)}
          pending={Boolean(tasksPending) && savedLabelNames.has(selected.name)}
          locked={countFor(selected.name) > 0 || (Boolean(tasksPending) && savedLabelNames.has(selected.name))}
        />
      ) : (
        <p className="text-xs text-slate-400 border-t border-slate-700 pt-3">Select a stage on the graph to edit its settings.</p>
      )}
    </aside>
  )
}
