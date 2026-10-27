import { labelDisplayName } from '../../lib/labelDisplay'
import { useRef, useState } from 'react'
import { api } from '../../api/client'
import type { Task, WorkflowLabel } from '../../api/client'
import ModalShell from '../shared/ModalShell'
import { forceMoveTargets } from '../../lib/forceMove'

// ForceMoveControl is the Task Detail escape hatch for a wedged task: a
// neutral link-style "Force move…" button that opens a dialog to move the task to
// ANY label of its workflow, bypassing the workflow's transition rules. It is
// intentionally separate from the normal "Move to…" control, which only offers
// legal targets. Errors (e.g. 409 while a run is live) render inline.
export default function ForceMoveControl({
  task,
  labels,
  onMoved,
}: {
  task: Task
  labels: WorkflowLabel[]
  onMoved: (updated: Task) => void
}) {
  const [open, setOpen] = useState(false)
  const [target, setTarget] = useState('')
  const [note, setNote] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const selectRef = useRef<HTMLSelectElement | null>(null)

  const targets = forceMoveTargets(labels, task.label)

  function close() {
    setOpen(false)
    setTarget('')
    setNote('')
    setError(null)
  }

  async function confirmMove() {
    if (!target) return
    setPending(true)
    setError(null)
    try {
      const updated = await api.tasks.forceMoveLabel(task.id, target, note.trim() || undefined)
      onMoved(updated)
      close()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="text-xs text-slate-400 hover:text-slate-200 underline-offset-2 hover:underline"
      >
        Force move…
      </button>
      {open && (
        <ModalShell
          onClose={close}
          ariaLabel="Force move task"
          initialFocusRef={selectRef}
          className="w-full max-w-md mx-4 bg-slate-900 border border-slate-700 rounded-lg p-4 flex flex-col gap-3"
        >
          <h2 className="text-sm font-semibold text-slate-100">Force move</h2>
          <p className="text-xs text-red-300 bg-red-950/40 border border-red-900/60 rounded p-2">
            Bypasses the workflow&apos;s rules. Use this when a task is stuck.
          </p>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Move to
            <select
              ref={selectRef}
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              className="bg-slate-800 border border-slate-700 rounded px-2 py-1 text-sm text-slate-100"
            >
              <option value="">Select a label…</option>
              {targets.map((l) => (
                <option key={l.name} value={l.name}>{labelDisplayName(l)}</option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-slate-400">
            Note (optional)
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={2}
              className="bg-slate-800 border border-slate-700 rounded px-2 py-1 text-sm text-slate-100"
            />
          </label>
          {error && <p role="alert" className="text-xs text-red-400">{error}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={close} className="text-xs px-3 py-1.5 rounded text-slate-300 hover:bg-slate-800">
              Cancel
            </button>
            <button
              type="button"
              onClick={confirmMove}
              disabled={!target || pending}
              className="text-xs px-3 py-1.5 rounded bg-red-600 text-white hover:bg-red-500 disabled:opacity-50"
            >
              {pending ? 'Moving…' : 'Force move'}
            </button>
          </div>
        </ModalShell>
      )}
    </div>
  )
}
