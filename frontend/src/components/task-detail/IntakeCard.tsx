import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type IntakeCheck, type Task, type Workflow } from '../../api/client'
import { errMessage } from '../../lib/errMessage'

type Props = {
  task: Task
  workflow: Workflow | null
  onChanged: (task: Task) => void
}

/**
 * Advisory intake card for a parked task: the assistant's clarity verdict,
 * questions (answers are appended to the description), suggested type, and
 * duplicate links. Nothing here runs by itself; every change is a click.
 */
export default function IntakeCard({ task, workflow, onChanged }: Props) {
  const [check, setCheck] = useState<IntakeCheck | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [error, setError] = useState('')
  const [answers, setAnswers] = useState<Record<number, string>>({})
  const [busy, setBusy] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const parked = !!workflow?.labels?.find((l) => l.name === task.label)?.agent_ignore
  const duplicates = task.possible_duplicates ?? []

  const load = useCallback(async () => {
    try {
      setCheck(await api.tasks.intakeCheck(task.id))
    } catch {
      setCheck(null)
    }
  }, [task.id])

  useEffect(() => {
    void load()
    api.intakeAssistant
      .get()
      .then((s) => setEnabled(s.enabled))
      .catch(() => {})
  }, [load])

  // Poll while a check is running.
  useEffect(() => {
    if (check?.status !== 'running') return
    timer.current = setTimeout(() => void load(), 2000)
    return () => {
      if (timer.current) clearTimeout(timer.current)
    }
  }, [check, load])

  if (task.archived || (!parked && !check && duplicates.length === 0)) return null

  const dupIds = new Set(check?.duplicate_ids ?? [])
  const shownDuplicates = duplicates.filter((d) => dupIds.has(d.id) || !check || check.status !== 'done' || dupIds.size === 0)

  async function act(fn: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await fn()
    } catch (e) {
      setError(errMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const runCheck = () =>
    act(async () => {
      setCheck(await api.tasks.runIntakeCheck(task.id))
    })

  const answer = (i: number, q: string) =>
    act(async () => {
      const text = (answers[i] ?? '').trim()
      if (!text) return
      const desc = `${task.description}${task.description ? '\n\n' : ''}Q: ${q}\nA: ${text}`
      onChanged(await api.tasks.update(task.id, { description: desc }))
      setAnswers((a) => ({ ...a, [i]: '' }))
    })

  const applyType = (type: string) => act(async () => onChanged(await api.tasks.update(task.id, { type })))

  const archiveDuplicate = () =>
    act(async () => {
      onChanged(await api.tasks.setArchived(task.id, true))
    })

  const wfName = check?.suggested_workflow_id
  return (
    <div data-testid="intake-card" className="border border-border bg-surface rounded-lg p-4 flex flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium text-fg">Intake</p>
        {enabled && (
          <button
            type="button"
            onClick={runCheck}
            disabled={busy || check?.status === 'running'}
            className="px-3 py-1 text-xs bg-accent hover:bg-accent/90 disabled:opacity-50 text-on-accent rounded-lg"
          >
            {check?.status === 'running' ? 'Checking…' : check ? 'Re-check task' : 'Check task'}
          </button>
        )}
      </div>

      {check?.status === 'failed' && <p className="text-xs text-danger">Check failed: {check.error}</p>}

      {check?.status === 'done' && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-fg-muted">
            Verdict:{' '}
            <span className={check.verdict === 'ready' ? 'text-success' : 'text-warning'}>
              {check.verdict === 'ready' ? 'ready' : 'needs detail'}
            </span>
            <span className="ml-2">(advisory · ${check.cost_usd.toFixed(3)})</span>
          </p>
          {check.questions.map((q, i) => (
            <div key={i} className="flex flex-col gap-1">
              <p className="text-sm text-fg">{q}</p>
              <div className="flex gap-2">
                <input
                  aria-label={`Answer: ${q}`}
                  value={answers[i] ?? ''}
                  onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
                  placeholder="Answer (appended to the description)"
                  className="flex-1 bg-surface-sunken border border-border rounded-lg px-2 py-1 text-sm text-fg"
                />
                <button
                  type="button"
                  onClick={() => answer(i, q)}
                  disabled={busy || !(answers[i] ?? '').trim()}
                  className="px-3 py-1 text-xs border border-border rounded-lg text-fg disabled:opacity-50"
                >
                  Add
                </button>
              </div>
            </div>
          ))}
          {check.suggested_type && check.suggested_type !== task.type && (
            <p className="text-xs text-fg-muted flex items-center gap-2">
              Suggested type: <b>{check.suggested_type}</b>
              <button
                type="button"
                onClick={() => applyType(check.suggested_type)}
                disabled={busy}
                className="px-2 py-0.5 text-xs border border-border rounded text-fg"
              >
                Apply
              </button>
            </p>
          )}
          {wfName && wfName !== task.workflow_id && (
            <p className="text-xs text-fg-muted">Suggested workflow id: {wfName} (move the task there by hand)</p>
          )}
        </div>
      )}

      {shownDuplicates.length > 0 && (
        <div className="flex flex-col gap-1">
          <p className="text-xs text-fg-muted">
            {check?.status === 'done' && dupIds.size > 0 ? 'Looks like a duplicate of:' : 'Possibly the same as:'}
          </p>
          {shownDuplicates.map((d) => (
            <div key={d.id} className="flex items-center gap-2 text-sm">
              <a href={`/tasks/${d.id}`} className="text-accent-fg hover:underline">
                {d.title}
              </a>
              {dupIds.has(d.id) && (
                <button
                  type="button"
                  onClick={archiveDuplicate}
                  disabled={busy}
                  className="px-2 py-0.5 text-xs border border-border rounded text-fg"
                >
                  Archive as duplicate
                </button>
              )}
            </div>
          ))}
        </div>
      )}
      {error && <p className="text-xs text-danger">{error}</p>}
    </div>
  )
}
