import { useState } from 'react'
import { api, type Task } from '../../api/client'

type Props = {
  task: Task
  /** Called with the refreshed task after the edit is applied or discarded. */
  onChanged: (task: Task) => void
}

function TextBlock({ label, text }: { label: string; text: string }) {
  return (
    <div className="min-w-0 flex-1">
      <p className="text-[11px] uppercase tracking-wide text-slate-500 mb-1">{label}</p>
      <pre className="text-xs text-slate-300 bg-slate-950/60 border border-slate-700 rounded p-2 whitespace-pre-wrap break-words max-h-48 overflow-y-auto">
        {text || '(empty)'}
      </pre>
    </div>
  )
}

/**
 * Shown when the source issue was edited after a human approved the task. The
 * importer holds the new text instead of overwriting the task (see
 * docs/task-sources.md); the human applies it or discards it here.
 */
export default function SourceUpdateNotice({ task, onChanged }: Props) {
  const pending = task.source_pending_update
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (!pending) return null

  const act = async (fn: () => Promise<Task>) => {
    setBusy(true)
    setError('')
    try {
      onChanged(await fn())
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div role="alert" data-testid="source-update-notice" className="border border-amber-700/60 bg-amber-950/30 rounded-lg p-4">
      <p className="text-sm font-medium text-amber-200">The source issue was edited after approval</p>
      <p className="text-xs text-amber-300/80 mt-1 mb-3">
        This task was already approved, so the new issue text has not been applied. Apply it only if you
        trust the edit; it will be what the agent sees from now on.
      </p>
      {pending.title !== task.title && (
        <div className="flex flex-col sm:flex-row gap-3 mb-3">
          <TextBlock label="Current title" text={task.title} />
          <TextBlock label="New title" text={pending.title} />
        </div>
      )}
      {pending.description !== task.description && (
        <div className="flex flex-col sm:flex-row gap-3 mb-3">
          <TextBlock label="Current description" text={task.description} />
          <TextBlock label="New description" text={pending.description} />
        </div>
      )}
      {pending.type !== task.type && (
        <p className="text-xs text-slate-400 mb-3">
          Type: <span className="text-slate-300">{task.type}</span> to <span className="text-slate-300">{pending.type}</span>
        </p>
      )}
      {error && <p role="alert" className="text-xs text-red-400 mb-2">{error}</p>}
      <div className="flex gap-2">
        <button
          type="button"
          disabled={busy}
          onClick={() => act(() => api.tasks.applySourceUpdate(task.id))}
          className="px-3 py-1.5 text-xs font-medium rounded text-white bg-sky-600 hover:bg-sky-500 disabled:opacity-50"
        >
          Apply
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={() => act(() => api.tasks.discardSourceUpdate(task.id))}
          className="px-3 py-1.5 text-xs font-medium rounded text-slate-100 bg-slate-700 hover:bg-slate-600 disabled:opacity-50"
        >
          Discard
        </button>
      </div>
    </div>
  )
}
