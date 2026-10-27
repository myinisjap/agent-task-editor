import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type LessonStatus, type RepoLesson } from '../../api/client'
import { LESSON_STATUSES, formatList } from '../../lib/lessons'
import LessonForm, { type LessonFormValues } from './LessonForm'

type Filter = LessonStatus | 'all'

const statusCls: Record<LessonStatus, string> = {
  proposed: 'bg-amber-900/60 text-amber-200',
  active: 'bg-emerald-900/60 text-emerald-200',
  stale: 'bg-slate-700 text-slate-300',
  retired: 'bg-slate-800 text-slate-500',
}

const btnCls = 'px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-200'

export default function LessonsPanel({ repoId, title = 'Lessons' }: { repoId: string | null; title?: string }) {
  const [lessons, setLessons] = useState<RepoLesson[]>([])
  const [filter, setFilter] = useState<Filter>('all')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({})
  const reqRef = useRef(0)

  const load = useCallback(() => {
    const req = ++reqRef.current
    const status = filter === 'all' ? undefined : filter
    setLoading(true)
    setError('')
    const p = repoId ? api.repos.lessons(repoId, status) : api.lessons.listGlobal(status)
    p.then((rows) => {
      if (req !== reqRef.current) return
      setLessons(rows ?? [])
    })
      .catch((e: any) => {
        if (req !== reqRef.current) return
        setError(e?.message ?? String(e))
      })
      .finally(() => {
        if (req === reqRef.current) setLoading(false)
      })
  }, [repoId, filter])

  useEffect(() => {
    load()
  }, [load])

  const matches = (l: RepoLesson) => filter === 'all' || l.status === filter

  const add = async (v: LessonFormValues) => {
    const body = { ...v, status: 'active' as const, source: 'human' as const }
    const created = repoId ? await api.repos.createLesson(repoId, body) : await api.lessons.createGlobal(body)
    if (matches(created)) setLessons((ls) => [created, ...ls])
    setAdding(false)
  }

  const setRowError = (id: string, msg: string) => setRowErrors((r) => ({ ...r, [id]: msg }))

  const applyUpdate = (updated: RepoLesson) => {
    setLessons((ls) => (matches(updated) ? ls.map((l) => (l.id === updated.id ? updated : l)) : ls.filter((l) => l.id !== updated.id)))
  }

  const changeStatus = async (l: RepoLesson, status: LessonStatus) => {
    setRowError(l.id, '')
    try {
      applyUpdate(await api.lessons.update(l.id, { status }))
    } catch (e: any) {
      setRowError(l.id, e?.message ?? String(e))
    }
  }

  const edit = async (l: RepoLesson, v: LessonFormValues) => {
    applyUpdate(await api.lessons.update(l.id, v))
    setEditingId(null)
  }

  const remove = async (l: RepoLesson) => {
    setRowError(l.id, '')
    try {
      await api.lessons.delete(l.id)
      setLessons((ls) => ls.filter((x) => x.id !== l.id))
      setConfirmDeleteId(null)
    } catch (e: any) {
      setRowError(l.id, e?.message ?? String(e))
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-slate-200">{title}</h3>
        <div className="flex flex-wrap items-center gap-2">
          <select
            aria-label="Filter by status"
            value={filter}
            onChange={(e) => setFilter(e.target.value as Filter)}
            className="bg-slate-800 border border-slate-700 rounded-lg px-2 py-1 text-xs text-slate-200"
          >
            <option value="all">All</option>
            {LESSON_STATUSES.map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
          <button onClick={() => setAdding((a) => !a)} className="px-3 py-1 text-xs rounded-lg bg-blue-600 hover:bg-blue-500 text-white">
            + Add lesson
          </button>
        </div>
      </div>

      {error && <p role="alert" className="text-xs text-red-400 break-words">{error}</p>}

      {adding && (
        <div className="border border-slate-700 rounded-lg p-3">
          <LessonForm submitLabel="Add lesson" onSubmit={add} onCancel={() => setAdding(false)} autoFocus />
        </div>
      )}

      {!loading && !error && lessons.length === 0 && (
        <p className="text-sm text-slate-500">{filter === 'all' ? 'No lessons yet.' : `No ${filter} lessons.`}</p>
      )}

      <ul className="flex flex-col gap-2">
        {lessons.map((l) => (
          <li key={l.id} className="border border-slate-700 rounded-lg p-3 flex flex-col gap-1 break-words">
            {editingId === l.id ? (
              <LessonForm
                initial={{ kind: l.kind, body: l.body, path_globs: l.path_globs, stages: l.stages }}
                submitLabel="Save"
                onSubmit={(v) => edit(l, v)}
                onCancel={() => setEditingId(null)}
              />
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="px-1.5 py-0.5 rounded text-[11px] bg-blue-900/60 text-blue-200">{l.kind}</span>
                  <span className={`px-1.5 py-0.5 rounded text-[11px] ${statusCls[l.status]}`}>{l.status}</span>
                  <span className="text-[11px] text-slate-500">Injected {l.injected_count}×</span>
                  {l.source_task_id && (
                    <Link to={`/tasks/${l.source_task_id}`} className="text-[11px] text-blue-400 hover:underline">
                      from task
                    </Link>
                  )}
                </div>
                <p className="text-sm text-slate-100 whitespace-pre-wrap">{l.body}</p>
                <p className="text-xs text-slate-500">
                  {l.path_globs.length ? `Paths: ${formatList(l.path_globs)}` : 'all files'}
                  {' · '}
                  {l.stages.length ? `Stages: ${formatList(l.stages)}` : 'all stages'}
                </p>
                <div className="flex flex-wrap gap-2 mt-1">
                  <button className={btnCls} onClick={() => { setEditingId(l.id); setRowError(l.id, '') }}>Edit</button>
                  {l.status !== 'retired' && (
                    <button className={btnCls} onClick={() => changeStatus(l, 'retired')}>Retire</button>
                  )}
                  {l.status !== 'active' && (
                    <button className={btnCls} onClick={() => changeStatus(l, 'active')}>Reactivate</button>
                  )}
                  {confirmDeleteId === l.id ? (
                    <>
                      <button className="px-2 py-1 text-xs rounded bg-red-700 hover:bg-red-600 text-white" onClick={() => remove(l)}>
                        Confirm delete
                      </button>
                      <button className={btnCls} onClick={() => setConfirmDeleteId(null)}>Cancel</button>
                    </>
                  ) : (
                    <button className={btnCls} onClick={() => setConfirmDeleteId(l.id)}>Delete</button>
                  )}
                </div>
              </>
            )}
            {rowErrors[l.id] && <p role="alert" className="text-xs text-red-400">{rowErrors[l.id]}</p>}
          </li>
        ))}
      </ul>
    </div>
  )
}
