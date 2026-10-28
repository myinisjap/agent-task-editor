import { useEffect, useMemo, useRef, useState } from 'react'
import { api, type AutonomySuggestions, type ShouldHaveAskedResponse, type Workflow } from '../../api/client'
import ModalShell from './ModalShell'
import { Button, Field, controlClasses } from '../ui'
import { labelDisplayNameByName } from '../../lib/labelDisplay'
import { useDebouncedCallback } from '../../lib/useDebouncedCallback'
import { LESSON_BODY_MAX } from '../../lib/lessons'
import { showError, showToast } from '../../stores/toast'
import {
  backtestLine,
  backtestSummary,
  buildRule,
  conditionOptions,
  lessonPrefill,
  successMessage,
  type BacktestSummary,
} from '../../lib/shouldHaveAsked'

type Props = {
  taskId: string
  taskTitle: string
  /** The task is on a terminal label, so it can't be sent back. */
  done: boolean
  workflow?: Workflow
  repoName?: string
  onClose: () => void
  onDone?: (resp: ShouldHaveAskedResponse) => void
}

const LEGEND = 'text-xs font-medium text-fg-muted mb-1.5'
const CHOICE = 'flex items-start gap-2 text-sm text-fg cursor-pointer'
const INPUT = 'mt-0.5 accent-[var(--color-accent)]'

type BacktestState = { status: 'idle' | 'pending' | 'error' } | { status: 'ok'; summary: BacktestSummary }

/** "Should have asked" dialog: turn an auto-approval the human disagrees with into an ask rule, send-back and lesson. */
export default function ShouldHaveAskedDialog({ taskId, taskTitle, done, workflow, repoName, onClose, onDone }: Props) {
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [sugg, setSugg] = useState<AutonomySuggestions | null>(null)
  const [loadError, setLoadError] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [thisRepo, setThisRepo] = useState(true)
  const [allGates, setAllGates] = useState(false)
  const [sendBack, setSendBack] = useState(false)
  const [note, setNote] = useState('')
  const [lessonOn, setLessonOn] = useState(false)
  const [lessonBody, setLessonBody] = useState('')
  const [lessonEdited, setLessonEdited] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')
  const [backtest, setBacktest] = useState<BacktestState>({ status: 'idle' })
  const seq = useRef(0)

  useEffect(() => {
    let cancelled = false
    api.autonomy.suggestions(taskId).then(
      (s) => { if (!cancelled) setSugg(s) },
      (e) => { if (!cancelled) setLoadError(e instanceof Error ? e.message : String(e)) },
    )
    return () => { cancelled = true }
  }, [taskId])

  const options = useMemo(() => (sugg ? conditionOptions(sugg) : []), [sugg])
  const chosen = options.filter((o) => selected.includes(o.id))
  const rule = buildRule(chosen)
  const globs = rule?.path_globs ?? []
  const hasRule = !!rule
  const selKey = selected.join('|')

  // Keep the lesson body in step with the ticked paths until the user edits it.
  useEffect(() => {
    if (!lessonEdited && globs.length > 0) setLessonBody(lessonPrefill(globs, taskTitle))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selKey, lessonEdited, taskTitle])

  const runBacktest = useDebouncedCallback(() => {
    if (!sugg) return
    const mine = ++seq.current
    const draft = buildRule(chosen, thisRepo ? sugg.repo_id : null)
    if (!draft) return
    const gates = allGates
      ? [sugg.label, ...(workflow?.labels ?? []).filter((l) => l.autonomy && l.autonomy.mode !== 'ask' && l.name !== sugg.label).map((l) => l.name)]
      : [sugg.label]
    Promise.all(gates.map((g) => api.autonomy.backtest(sugg.workflow_id, g, { mode: 'ask', days: 30, extra_rules: [draft] }))).then(
      (rs) => { if (mine === seq.current) setBacktest({ status: 'ok', summary: backtestSummary(rs.map((r) => r.rules), taskId) }) },
      () => { if (mine === seq.current) setBacktest({ status: 'error' }) },
    )
  }, 400)

  useEffect(() => {
    if (!sugg) return
    if (!hasRule) {
      seq.current++
      setBacktest({ status: 'idle' })
      return
    }
    setBacktest({ status: 'pending' })
    runBacktest()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sugg, selKey, thisRepo, allGates])

  const toggle = (id: string) => setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]))
  const showSendBack = !done
  const needsNote = showSendBack && sendBack && note.trim() === ''
  const lessonAvailable = globs.length > 0
  const lessonActive = lessonOn && lessonAvailable
  const needsLesson = lessonActive && lessonBody.trim() === ''
  const blocked = needsNote || needsLesson
  const gateName = sugg ? labelDisplayNameByName(sugg.label, workflow?.labels) : ''

  async function submit() {
    if (!sugg || blocked || submitting) return
    setSubmitting(true)
    setSubmitError('')
    try {
      const resp = await api.autonomy.shouldHaveAsked(taskId, {
        rule,
        scope_repo: thisRepo,
        label_scope: allGates ? 'all_gates' : 'this_gate',
        send_back_note: showSendBack && sendBack ? note.trim() : undefined,
        lesson: lessonActive ? { body: lessonBody.trim(), kind: 'trap' } : undefined,
      })
      const { message, error } = successMessage(resp, resp.step_down ? labelDisplayNameByName(resp.step_down.label, workflow?.labels) : undefined)
      if (error) showError(message)
      else showToast(message)
      onDone?.(resp)
      onClose()
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : String(e))
      setSubmitting(false)
    }
  }

  let body
  if (loadError) {
    body = <p role="alert" className="text-sm text-danger">{loadError}</p>
  } else if (!sugg) {
    body = <p className="text-sm text-fg-muted">Loading suggestions…</p>
  } else if (!sugg.policy_approved) {
    body = <p className="text-sm text-fg-muted">The policy didn't approve this task, so there is nothing to flag.</p>
  } else {
    body = (
      <div className="flex flex-col gap-4">
        <fieldset>
          <legend className={LEGEND}>What should have stopped it?</legend>
          <div className="flex flex-col gap-1.5">
            {options.map((o) => (
              <label key={o.id} className={CHOICE}>
                <input type="checkbox" className={INPUT} checked={selected.includes(o.id)} onChange={() => toggle(o.id)} />
                <span>{o.prefix}{o.code && <code className="font-mono text-xs bg-surface-sunken rounded px-1">{o.code}</code>}{o.suffix}</span>
              </label>
            ))}
            <label className={CHOICE}>
              <input type="checkbox" className={INPUT} checked={selected.length === 0} onChange={() => setSelected([])} />
              <span>Nothing specific, just log it</span>
            </label>
          </div>
          {!sugg.diff_available && <p className="text-xs text-fg-subtle mt-1.5">No diff available, so no path suggestions.</p>}
          {globs.length >= 2 && <p className="text-xs text-fg-subtle mt-1.5">Matches if any ticked path changed.</p>}
        </fieldset>

        <fieldset disabled={!hasRule} className={hasRule ? '' : 'opacity-50'}>
          <legend className={LEGEND}>Applies to</legend>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5" role="radiogroup" aria-label="Repos">
              <label className={CHOICE}>
                <input type="radio" name="sha-repo" className={INPUT} checked={thisRepo} onChange={() => setThisRepo(true)} />
                <span>This repo{repoName ? ` (${repoName})` : ''}</span>
              </label>
              <label className={CHOICE}>
                <input type="radio" name="sha-repo" className={INPUT} checked={!thisRepo} onChange={() => setThisRepo(false)} />
                <span>All repos</span>
              </label>
            </div>
            <div className="flex flex-col gap-1.5" role="radiogroup" aria-label="Gates">
              <label className={CHOICE}>
                <input type="radio" name="sha-gate" className={INPUT} checked={!allGates} onChange={() => setAllGates(false)} />
                <span>This gate ({gateName})</span>
              </label>
              <label className={CHOICE}>
                <input type="radio" name="sha-gate" className={INPUT} checked={allGates} onChange={() => setAllGates(true)} />
                <span>All gates</span>
              </label>
            </div>
          </div>
        </fieldset>

        <p aria-live="polite" data-testid="sha-backtest" className="text-xs text-fg-muted min-h-4">
          {hasRule && backtest.status === 'pending' && 'Checking the last 30 days…'}
          {hasRule && backtest.status === 'error' && "Couldn't check recent approvals."}
          {hasRule && backtest.status === 'ok' && backtestLine(backtest.summary)}
        </p>

        {showSendBack && (
          <fieldset>
            <legend className={LEGEND}>This task</legend>
            <div className="flex flex-col gap-1.5">
              <label className={CHOICE}>
                <input type="radio" name="sha-task" className={INPUT} checked={!sendBack} onChange={() => setSendBack(false)} />
                <span>Leave it</span>
              </label>
              <label className={CHOICE}>
                <input type="radio" name="sha-task" className={INPUT} checked={sendBack} onChange={() => setSendBack(true)} />
                <span>Send back to work with a note</span>
              </label>
            </div>
            {sendBack && (
              <Field label="Note for the agent" className="mt-2">
                <textarea className={controlClasses} rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
              </Field>
            )}
          </fieldset>
        )}

        {lessonAvailable && (
          <fieldset>
            <legend className="sr-only">Lesson</legend>
            <label className={CHOICE}>
              <input type="checkbox" className={INPUT} checked={lessonOn} onChange={(e) => setLessonOn(e.target.checked)} />
              <span>Also tell the agent: save a lesson for these paths</span>
            </label>
            {lessonOn && (
              <Field label="Lesson" hint={`${lessonBody.length}/${LESSON_BODY_MAX}`} className="mt-2">
                <textarea
                  className={controlClasses}
                  rows={3}
                  maxLength={LESSON_BODY_MAX}
                  value={lessonBody}
                  onChange={(e) => { setLessonEdited(true); setLessonBody(e.target.value) }}
                />
              </Field>
            )}
          </fieldset>
        )}
        {submitError && <p role="alert" className="text-xs text-danger">{submitError}</p>}
      </div>
    )
  }

  const canSubmit = !!sugg && sugg.policy_approved && !loadError

  return (
    <ModalShell
      onClose={onClose}
      ariaLabel="Should have asked"
      testId="should-have-asked-dialog"
      initialFocusRef={headingRef}
      overlayClassName="fixed inset-0 z-50 flex items-end md:items-center justify-center bg-black/60"
      className="w-full md:max-w-lg max-h-[90vh] overflow-y-auto bg-surface-raised border border-border rounded-t-xl md:rounded-xl shadow-xl p-4"
    >
      <h2 ref={headingRef} tabIndex={-1} className="text-sm font-semibold text-fg outline-none">Should have asked</h2>
      <p className="text-xs text-fg-subtle mt-0.5 mb-3 break-words">{taskTitle}</p>
      {body}
      <div className="flex justify-end gap-2 mt-4">
        <Button variant="secondary" onClick={onClose}>Cancel</Button>
        {canSubmit && (
          <Button variant="primary" loading={submitting} disabled={blocked} onClick={() => void submit()}>
            {hasRule ? 'Add rule' : 'Log it'}
          </Button>
        )}
      </div>
    </ModalShell>
  )
}
