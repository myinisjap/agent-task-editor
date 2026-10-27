import { useId, useState, type FormEvent } from 'react'
import type { LessonKind } from '../../api/client'
import { LESSON_BODY_MAX, LESSON_KINDS, formatList, parseList } from '../../lib/lessons'

export interface LessonFormValues {
  kind: LessonKind
  body: string
  path_globs: string[]
  stages: string[]
}

const inputCls =
  'w-full bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 focus:outline-none focus:border-blue-500'

export default function LessonForm({
  initial,
  submitLabel,
  onSubmit,
  onCancel,
  showScope = true,
  autoFocus,
}: {
  initial?: Partial<LessonFormValues>
  submitLabel: string
  onSubmit: (values: LessonFormValues) => Promise<void>
  onCancel: () => void
  showScope?: boolean
  autoFocus?: boolean
}) {
  const uid = useId()
  const [kind, setKind] = useState<LessonKind>(initial?.kind ?? 'convention')
  const [body, setBody] = useState(initial?.body ?? '')
  const [globs, setGlobs] = useState(formatList(initial?.path_globs ?? []))
  const [stages, setStages] = useState(formatList(initial?.stages ?? []))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!body.trim() || saving) return
    setSaving(true)
    setError('')
    try {
      await onSubmit({ kind, body: body.trim(), path_globs: parseList(globs), stages: parseList(stages) })
    } catch (err: any) {
      setError(err?.message ?? String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <label htmlFor={`${uid}-kind`} className="text-xs text-slate-400">Kind</label>
        <select id={`${uid}-kind`} value={kind} onChange={(e) => setKind(e.target.value as LessonKind)} className={inputCls}>
          {LESSON_KINDS.map((k) => (
            <option key={k} value={k}>{k}</option>
          ))}
        </select>
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor={`${uid}-body`} className="text-xs text-slate-400">Lesson</label>
        <textarea
          id={`${uid}-body`}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          maxLength={LESSON_BODY_MAX}
          rows={4}
          autoFocus={autoFocus}
          className={inputCls}
        />
        <span className="text-[11px] text-slate-500 text-right">{body.length}/{LESSON_BODY_MAX}</span>
      </div>
      {showScope && (
        <>
          <div className="flex flex-col gap-1">
            <label htmlFor={`${uid}-globs`} className="text-xs text-slate-400">Path globs</label>
            <input id={`${uid}-globs`} value={globs} onChange={(e) => setGlobs(e.target.value)} className={inputCls} />
            <span className="text-[11px] text-slate-500">Comma-separated. Blank = always applies; e.g. backend/**, *.sql</span>
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor={`${uid}-stages`} className="text-xs text-slate-400">Stages</label>
            <input id={`${uid}-stages`} value={stages} onChange={(e) => setStages(e.target.value)} className={inputCls} />
            <span className="text-[11px] text-slate-500">Comma-separated workflow labels. Blank = all agents; e.g. work, review</span>
          </div>
        </>
      )}
      {error && <p role="alert" className="text-xs text-red-400 break-words">{error}</p>}
      <div className="flex flex-wrap justify-end gap-2">
        <button type="button" onClick={onCancel} className="px-3 py-1.5 text-sm rounded-lg bg-slate-700 hover:bg-slate-600 text-slate-200">
          Cancel
        </button>
        <button
          type="submit"
          disabled={saving || !body.trim()}
          className="px-3 py-1.5 text-sm rounded-lg bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-50"
        >
          {saving ? 'Saving…' : submitLabel}
        </button>
      </div>
    </form>
  )
}
