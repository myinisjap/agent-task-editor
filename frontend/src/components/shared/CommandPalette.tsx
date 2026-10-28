import { useEffect, useMemo, useRef, useState } from 'react'
import { Command } from 'cmdk'
import { useNavigate } from 'react-router-dom'
import { api, type Task } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { useCommandPaletteStore, type TaskCommand } from '../../stores/commandPalette'
import { REVIEW_QUEUE_HREF } from '../../lib/inbox'
import { PAGES, formatKeys, isMacPlatform, shortcutKeys } from '../../lib/shortcuts'
import { useDebouncedCallback } from '../../lib/useDebouncedCallback'

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

const itemCls =
  'flex items-center justify-between gap-3 rounded-md px-3 py-2 text-sm text-slate-300 cursor-pointer ' +
  'data-[selected=true]:bg-slate-700 data-[selected=true]:text-slate-50'
const headingCls = '[&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:py-1 [&_[cmdk-group-heading]]:text-xs ' +
  '[&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:text-slate-500'

function Hint({ id }: { id?: string }) {
  const keys = id ? shortcutKeys(id) : undefined
  if (!keys) return null
  // Single-key shortcuts don't exist on touch devices, so hide the hint there.
  return (
    <kbd className="hidden [@media(hover:hover)]:inline rounded border border-slate-600 bg-slate-800 px-1.5 py-0.5 text-xs text-slate-300 whitespace-nowrap">
      {formatKeys(keys, isMacPlatform())}
    </kbd>
  )
}

function matches(q: string, ...parts: (string | undefined)[]): boolean {
  if (!q) return true
  const hay = parts.filter(Boolean).join(' ').toLowerCase()
  return q.toLowerCase().split(/\s+/).every((w) => hay.includes(w))
}

export default function CommandPalette({ onError }: { onError: (msg: string) => void }) {
  const open = useCommandPaletteStore((s) => s.open)
  const setOpen = useCommandPaletteStore((s) => s.setOpen)
  const taskContext = useCommandPaletteStore((s) => s.taskContext)
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Task[]>([])
  const [searching, setSearching] = useState(false)
  const reqRef = useRef(0)

  useEffect(() => {
    if (!open) { setQuery(''); setResults([]); setSearching(false); reqRef.current++ }
  }, [open])

  const search = useDebouncedCallback(async (q: string) => {
    const my = ++reqRef.current
    setSearching(true)
    try {
      const [page, byId] = await Promise.all([
        api.tasks.list({ q }, { limit: 10 }).catch(() => null),
        UUID_RE.test(q) ? api.tasks.get(q).catch(() => null) : Promise.resolve(null),
      ])
      if (my !== reqRef.current) return
      const merged: Task[] = []
      for (const t of [...(byId ? [byId] : []), ...(page?.items ?? [])]) {
        if (!merged.some((m) => m.id === t.id)) merged.push(t)
      }
      setResults(merged)
    } finally {
      if (my === reqRef.current) setSearching(false)
    }
  }, 200)

  const onQuery = (v: string) => {
    setQuery(v)
    const q = v.trim()
    if (q.length >= 2) {
      search(q)
    } else {
      reqRef.current++
      setResults([])
      setSearching(false)
    }
  }

  const q = query.trim()
  const tasks = useMemo(() => {
    const merged = [...results]
    if (q.length >= 2) {
      const lower = q.toLowerCase()
      for (const t of useTasksStore.getState().tasks) {
        if (t.id.toLowerCase().startsWith(lower) && !merged.some((m) => m.id === t.id)) merged.unshift(t)
      }
    }
    return merged.slice(0, 12)
  }, [results, q])

  const close = () => setOpen(false)
  const run = (fn: () => void | Promise<void>) => {
    close()
    Promise.resolve()
      .then(fn)
      .catch((e: unknown) => onError(e instanceof Error ? e.message : String(e)))
  }

  const actions: { id: string; label: string; shortcut: string; keywords?: string; fn: () => void }[] = [
    { id: 'review-queue', label: 'Review queue', shortcut: 'review-queue', keywords: 'inbox needs you approve', fn: () => navigate(REVIEW_QUEUE_HREF) },
    { id: 'new-task', label: 'Create task', shortcut: 'new-task', fn: () => useCommandPaletteStore.getState().setNewTaskOpen(true) },
    { id: 'help', label: 'Keyboard shortcuts', shortcut: 'help', fn: () => useCommandPaletteStore.getState().setSheetOpen(true) },
  ].filter((a) => matches(q, a.label, a.keywords))
  const taskCmds: TaskCommand[] = (taskContext?.commands ?? []).filter((c) => matches(q, c.label, c.keywords))
  const pages = PAGES.filter((p) => matches(q, p.label, p.keywords))
  const empty = !searching && tasks.length === 0 && actions.length === 0 && taskCmds.length === 0 && pages.length === 0

  return (
    <Command.Dialog
      open={open}
      onOpenChange={setOpen}
      label="Search and commands"
      shouldFilter={false}
      overlayClassName="fixed inset-0 z-50 bg-black/60"
      contentClassName="fixed z-50 left-0 right-0 top-0 md:left-1/2 md:right-auto md:top-24 md:-translate-x-1/2 w-full md:max-w-xl rounded-b-lg md:rounded-lg border border-slate-700 bg-slate-900 shadow-2xl overflow-hidden"
    >
      <Command.Input
        value={query}
        onValueChange={onQuery}
        placeholder="Search tasks or type a command…"
        className="w-full bg-transparent border-b border-slate-700 px-4 py-3 text-base md:text-sm text-slate-100 placeholder-slate-500 focus:outline-none"
      />
      <Command.List className="max-h-[60vh] overflow-y-auto p-2">
        {taskContext && taskCmds.length > 0 && (
          <Command.Group heading={`Current task: ${taskContext.title}`} className={headingCls}>
            {taskCmds.map((c) => (
              <Command.Item key={c.id} value={`task-cmd:${c.id}`} onSelect={() => run(c.run)} className={itemCls}>
                {c.label}
              </Command.Item>
            ))}
          </Command.Group>
        )}
        {q.length >= 2 && (tasks.length > 0 || searching) && (
          <Command.Group heading="Tasks" className={headingCls}>
            {searching && tasks.length === 0 && <Command.Loading>Searching…</Command.Loading>}
            {tasks.map((t) => (
              <Command.Item key={t.id} value={`task:${t.id}`} onSelect={() => run(() => navigate(`/tasks/${t.id}`))} className={itemCls}>
                <span className="truncate">{t.title}</span>
                <span className="shrink-0 text-xs text-slate-500">
                  <span className="font-mono">{t.id.slice(0, 8)}</span> · {t.label}
                </span>
              </Command.Item>
            ))}
          </Command.Group>
        )}
        {actions.length > 0 && (
          <Command.Group heading="Actions" className={headingCls}>
            {actions.map((a) => (
              <Command.Item key={a.id} value={`action:${a.id}`} onSelect={() => run(a.fn)} className={itemCls}>
                {a.label}
                <Hint id={a.shortcut} />
              </Command.Item>
            ))}
          </Command.Group>
        )}
        {pages.length > 0 && (
          <Command.Group heading="Go to" className={headingCls}>
            {pages.map((p) => (
              <Command.Item key={p.to} value={`page:${p.to}`} onSelect={() => run(() => navigate(p.to))} className={itemCls}>
                {p.label}
                <Hint id={p.shortcutId} />
              </Command.Item>
            ))}
          </Command.Group>
        )}
        {empty && <Command.Empty className="px-3 py-6 text-center text-sm text-slate-500">No results</Command.Empty>}
      </Command.List>
    </Command.Dialog>
  )
}
