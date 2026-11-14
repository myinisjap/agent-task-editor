import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type LessonDistillRun, type RepoLesson } from '../../api/client'
import { formatList } from '../../lib/lessons'
import LessonForm, { type LessonFormValues } from './LessonForm'

interface Citation {
  task_id: string
  title: string
  kind: string
  quote: string
}

const btnCls = 'px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-200'

const citationsOf = (l: RepoLesson): Citation[] =>
  (Array.isArray(l.citations) ? (l.citations as Citation[]) : []).filter((c) => c && typeof c.task_id === 'string')

/**
 * Lessons drafted by the distiller. They are inert (never injected) until
 * accepted here. Quiet by design: not part of the needs-you count.
 */
export default function SuggestedLessons({ repoId, onAccepted }: { repoId: string; onAccepted: () => void }) {
  const [items, setItems] = useState<RepoLesson[]>([])
  const [lastRun, setLastRun] = useState<LessonDistillRun | null>(null)
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)
  const [pollTick, setPollTick] = useState(0)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({})
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const load = useCallback(async () => {
    try {
      const [rows, runs] = await Promise.all([api.repos.lessons(repoId, 'proposed'), api.repos.lessonDistillRuns(repoId, 1)])
      setItems((rows ?? []).filter((l) => l.source === 'distiller'))
      const run = (runs ?? [])[0] ?? null
      setLastRun(run)
      return run
    } catch (e: any) {
      setError(e?.message ?? String(e))
      return null
    }
  }, [repoId])

  useEffect(() => {
    load().then((run) => {
      if (run?.status === 'running') setRunning(true)
    })
    return () => {
      if (timer.current) clearTimeout(timer.current)
    }
  }, [load])

  // Poll while a run is in progress.
  useEffect(() => {
    if (!running) return
    timer.current = setTimeout(async () => {
      const run = await load()
      if (run?.status === 'running') setPollTick((t) => t + 1)
      else setRunning(false)
    }, 3000)
    return () => {
      if (timer.current) clearTimeout(timer.current)
    }
  }, [running, pollTick, load])

  const suggest = async () => {
    setError('')
    try {
      setLastRun(await api.repos.distillLessons(repoId))
      setRunning(true)
    } catch (e: any) {
      setError(e?.message ?? String(e))
    }
  }

  const setRowError = (id: string, msg: string) => setRowErrors((r) => ({ ...r, [id]: msg }))

  const accept = async (l: RepoLesson, v?: LessonFormValues) => {
    setRowError(l.id, '')
    try {
      await api.lessons.update(l.id, { ...(v ?? {}), status: 'active' })
      setItems((xs) => xs.filter((x) => x.id !== l.id))
      setEditingId(null)
      onAccepted()
    } catch (e: any) {
      setRowError(l.id, e?.message ?? String(e))
      throw e
    }
  }

  const dismiss = async (l: RepoLesson) => {
    setRowError(l.id, '')
    try {
      await api.lessons.dismiss(l.id)
      setItems((xs) => xs.filter((x) => x.id !== l.id))
    } catch (e: any) {
      setRowError(l.id, e?.message ?? String(e))
    }
  }

  const runSummary = lastRun
    ? lastRun.status === 'running'
      ? 'Running…'
      : `Last run ${new Date(lastRun.started_at).toLocaleString()} · $${lastRun.cost_usd.toFixed(2)}` +
        (lastRun.status === 'failed' ? ` · failed: ${lastRun.error}` : lastRun.status === 'skipped' ? ` · ${lastRun.error}` : '')
    : 'Never run'

  return (
    <section aria-label="Suggested lessons" className="flex flex-col gap-2 border border-slate-700 rounded-lg p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-sm font-semibold text-slate-200 flex items-center gap-2">
          Suggested
          {items.length > 0 && (
            <span className="px-1.5 py-0.5 rounded-full text-[11px] bg-amber-900/60 text-amber-200">{items.length}</span>
          )}
        </h4>
        <div className="flex items-center gap-2">
          <span className="text-[11px] text-slate-500">{runSummary}</span>
          <button className={btnCls} onClick={suggest} disabled={running}>
            Suggest lessons
          </button>
        </div>
      </div>
      <p className="text-[11px] text-slate-500">Drafted from recent rejections, replies and failures. Not used by agents until you accept.</p>
      {error && <p role="alert" className="text-xs text-red-400 break-words">{error}</p>}
      <ul className="flex flex-col gap-2">
        {items.map((l) => (
          <li key={l.id} className="border border-slate-700 rounded-lg p-3 flex flex-col gap-1 break-words">
            {editingId === l.id ? (
              <LessonForm
                initial={{ kind: l.kind, body: l.body, path_globs: l.path_globs, stages: l.stages }}
                submitLabel="Accept"
                onSubmit={(v) => accept(l, v)}
                onCancel={() => setEditingId(null)}
              />
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="px-1.5 py-0.5 rounded text-[11px] bg-blue-900/60 text-blue-200">{l.kind}</span>
                </div>
                <p className="text-sm text-slate-100 whitespace-pre-wrap">{l.body}</p>
                <p className="text-xs text-slate-500">
                  {l.path_globs.length ? `Paths: ${formatList(l.path_globs)}` : 'all files'}
                  {' · '}
                  {l.stages.length ? `Stages: ${formatList(l.stages)}` : 'all stages'}
                </p>
                <ul className="flex flex-col gap-1">
                  {citationsOf(l).map((c) => (
                    <li key={c.task_id} className="text-xs text-slate-400">
                      <Link to={`/tasks/${c.task_id}`} className="text-blue-400 hover:underline">{c.title || c.task_id}</Link>
                      {c.quote && <q className="ml-1 italic text-slate-400">{c.quote}</q>}
                    </li>
                  ))}
                </ul>
                <div className="flex flex-wrap gap-2 mt-1">
                  <button className="px-2 py-1 text-xs rounded bg-emerald-600 hover:bg-emerald-500 text-white" onClick={() => accept(l).catch(() => {})}>
                    Accept
                  </button>
                  <button className={btnCls} onClick={() => { setEditingId(l.id); setRowError(l.id, '') }}>Edit and accept</button>
                  <button className={btnCls} onClick={() => dismiss(l)}>Dismiss</button>
                </div>
              </>
            )}
            {rowErrors[l.id] && <p role="alert" className="text-xs text-red-400">{rowErrors[l.id]}</p>}
          </li>
        ))}
      </ul>
    </section>
  )
}
