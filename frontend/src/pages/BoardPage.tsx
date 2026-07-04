import { useEffect, useMemo, useState } from 'react'
import { useTasksStore } from '../stores/tasks'
import { useWorkflowStore } from '../stores/workflow'
import { useReposStore } from '../stores/repos'
import TaskBoard from '../components/board/TaskBoard'
import NewTaskModal from '../components/board/NewTaskModal'
import { api, type BulkAction } from '../api/client'
import { wsClient } from '../api/ws'

const CONDENSED_STORAGE_KEY = 'board.condensed'

const TASK_TYPES = ['feature', 'bug', 'chore', 'spike']
const GIT_STATES = ['pushed', 'pr_open', 'pr_merged', 'pr_closed']

export default function BoardPage() {
  const { tasks, loading, fetch: fetchTasks, upsert } = useTasksStore()
  const { workflows, fetch: fetchWorkflows, setSelectedId, active } = useWorkflowStore()
  const { repos, fetch: fetchRepos } = useReposStore()
  const [runningTaskIds] = useState(() => new Set<string>())
  // Map of taskId → ISO unblocked_at string for tasks blocked by API rate limits
  const [rateLimitedTaskIds, setRateLimitedTaskIds] = useState(() => new Map<string, string>())
  const [showNewTask, setShowNewTask] = useState(false)
  const [condensed, setCondensed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(CONDENSED_STORAGE_KEY) === 'true'
    } catch {
      return false
    }
  })

  // Board filters. Search/repo/type/git-state are applied client-side over
  // the fetched list; the archived toggle changes what we fetch (archived
  // tasks are excluded from the default GET /tasks response).
  const [search, setSearch] = useState('')
  const [filterRepo, setFilterRepo] = useState('')
  const [filterType, setFilterType] = useState('')
  const [filterGitState, setFilterGitState] = useState('')
  const [showArchived, setShowArchived] = useState(false)

  // Multi-select for bulk actions: ids of selected task cards.
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set())
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkError, setBulkError] = useState('')

  const toggleCondensed = () => {
    setCondensed((prev) => {
      const next = !prev
      try {
        localStorage.setItem(CONDENSED_STORAGE_KEY, String(next))
      } catch {
        // ignore storage errors
      }
      return next
    })
  }

  useEffect(() => {
    fetchTasks(showArchived ? { archived: 'all' } : undefined)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showArchived])

  useEffect(() => {
    fetchWorkflows()
    fetchRepos()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const off = wsClient.on((event) => {
      if (event.type === 'task.label_changed' || event.type === 'task.updated' || event.type === 'task.created' || event.type === 'task.git_state_changed') {
        // Refresh the task from API to get latest data
        const taskId = event.type === 'task.created' ? event.payload.id :
                       event.type === 'task.updated' ? event.payload.id : event.payload.task_id
        api.tasks.get(taskId).then(upsert).catch(() => {})
      }
      if (event.type === 'task.rate_limited') {
        setRateLimitedTaskIds(prev => {
          const next = new Map(prev)
          next.set(event.payload.task_id, event.payload.unblocked_at)
          return next
        })
      }
      if (event.type === 'task.agent_started') {
        // Clear rate-limit badge when the agent successfully starts again
        setRateLimitedTaskIds(prev => {
          const next = new Map(prev)
          next.delete(event.payload.task_id)
          return next
        })
      }
    })
    return off
  }, [upsert])

  const workflow = active()
  const labels = workflow?.labels ?? []
  const transitions = workflow?.transitions ?? []

  // Filter tasks to the active workflow, then apply the board filters.
  const filteredTasks = useMemo(() => {
    const q = search.trim().toLowerCase()
    return tasks.filter((t) => {
      if (workflow && t.workflow_id !== workflow.id) return false
      if (!showArchived && t.archived) return false
      if (filterRepo && t.repo_id !== filterRepo) return false
      if (filterType && t.type !== filterType) return false
      if (filterGitState && (t.git_state ?? '') !== filterGitState) return false
      if (q && !t.title.toLowerCase().includes(q) && !(t.description ?? '').toLowerCase().includes(q)) return false
      return true
    })
  }, [tasks, workflow, search, filterRepo, filterType, filterGitState, showArchived])

  const hasFilters = search !== '' || filterRepo !== '' || filterType !== '' || filterGitState !== '' || showArchived

  const clearFilters = () => {
    setSearch('')
    setFilterRepo('')
    setFilterType('')
    setFilterGitState('')
    setShowArchived(false)
  }

  const toggleSelect = (taskId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (next.has(taskId)) next.delete(taskId)
      else next.add(taskId)
      return next
    })
  }

  const runBulk = async (action: BulkAction, toLabel?: string) => {
    if (selectedIds.size === 0) return
    setBulkBusy(true)
    setBulkError('')
    try {
      const { results } = await api.tasks.bulk([...selectedIds], action, toLabel ? { to_label: toLabel } : undefined)
      const failed = results.filter((r) => !r.ok)
      if (failed.length > 0) {
        setBulkError(`${failed.length} of ${results.length} failed: ${failed[0].error}`)
      } else {
        setSelectedIds(new Set())
      }
      await fetchTasks(showArchived ? { archived: 'all' } : undefined)
    } catch (e) {
      setBulkError(String(e))
    } finally {
      setBulkBusy(false)
    }
  }

  const selectCls = 'bg-slate-800 border border-slate-700 rounded px-2 py-1 text-xs text-slate-300 focus:outline-none focus:ring-1 focus:ring-indigo-500 cursor-pointer'

  return (
    <div className="p-6 h-full flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-xl font-semibold text-slate-100">Board</h1>
        <div className="flex items-center gap-3">
          {workflows.length > 0 && (
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-slate-500">Workflow:</span>
              <select
                value={workflow?.id ?? ''}
                onChange={(e) => setSelectedId(e.target.value)}
                className={selectCls}
              >
                {workflows.map((wf) => (
                  <option key={wf.id} value={wf.id}>
                    {wf.name}
                  </option>
                ))}
              </select>
            </div>
          )}
          <button
            onClick={toggleCondensed}
            title={condensed ? 'Switch to expanded view' : 'Switch to condensed view'}
            className={`flex items-center gap-1.5 text-xs px-2.5 py-1.5 rounded-md border transition-colors ${
              condensed
                ? 'bg-indigo-700 border-indigo-500 text-indigo-100 hover:bg-indigo-600'
                : 'bg-slate-800 border-slate-700 text-slate-400 hover:border-slate-500 hover:text-slate-200'
            }`}
          >
            <span>{condensed ? '⊟' : '⊞'}</span>
            <span>{condensed ? 'Expanded' : 'Condensed'}</span>
          </button>
        </div>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search tasks…"
          className="bg-slate-800 border border-slate-700 rounded px-2.5 py-1 text-xs text-slate-200 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500 w-48"
        />
        <select value={filterRepo} onChange={(e) => setFilterRepo(e.target.value)} className={selectCls}>
          <option value="">All repos</option>
          {repos.map((r) => (
            <option key={r.id} value={r.id}>{r.name}</option>
          ))}
        </select>
        <select value={filterType} onChange={(e) => setFilterType(e.target.value)} className={selectCls}>
          <option value="">All types</option>
          {TASK_TYPES.map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <select value={filterGitState} onChange={(e) => setFilterGitState(e.target.value)} className={selectCls}>
          <option value="">Any git state</option>
          {GIT_STATES.map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
        <button
          onClick={() => setShowArchived((v) => !v)}
          className={`text-xs px-2.5 py-1 rounded border transition-colors ${
            showArchived
              ? 'bg-indigo-700 border-indigo-500 text-indigo-100 hover:bg-indigo-600'
              : 'bg-slate-800 border-slate-700 text-slate-400 hover:border-slate-500 hover:text-slate-200'
          }`}
          title="Show archived tasks on the board"
        >
          🗄 Archived
        </button>
        {hasFilters && (
          <button
            onClick={clearFilters}
            className="text-xs px-2 py-1 text-slate-500 hover:text-slate-300 transition-colors"
          >
            Clear filters
          </button>
        )}
      </div>

      {/* Bulk action toolbar — appears while cards are selected */}
      {selectedIds.size > 0 && (
        <div className="flex flex-wrap items-center gap-2 mb-4 px-3 py-2 bg-slate-800/80 border border-slate-700 rounded-lg">
          <span className="text-xs font-medium text-slate-300">{selectedIds.size} selected</span>
          <select
            defaultValue=""
            disabled={bulkBusy}
            onChange={(e) => {
              if (e.target.value) {
                runBulk('move', e.target.value)
                e.target.value = ''
              }
            }}
            className={selectCls}
          >
            <option value="" disabled>Move to…</option>
            {[...labels].sort((a, b) => a.sort_order - b.sort_order).map((l) => (
              <option key={l.id} value={l.name}>{l.name}</option>
            ))}
          </select>
          <button disabled={bulkBusy} onClick={() => runBulk('pause')} className="text-xs px-2.5 py-1 rounded border bg-slate-800 border-slate-700 text-slate-300 hover:border-slate-500 disabled:opacity-50 transition-colors">⏸ Pause</button>
          <button disabled={bulkBusy} onClick={() => runBulk('resume')} className="text-xs px-2.5 py-1 rounded border bg-slate-800 border-slate-700 text-slate-300 hover:border-slate-500 disabled:opacity-50 transition-colors">▶ Resume</button>
          <button disabled={bulkBusy} onClick={() => runBulk('archive')} className="text-xs px-2.5 py-1 rounded border bg-slate-800 border-slate-700 text-slate-300 hover:border-slate-500 disabled:opacity-50 transition-colors">🗄 Archive</button>
          {showArchived && (
            <button disabled={bulkBusy} onClick={() => runBulk('unarchive')} className="text-xs px-2.5 py-1 rounded border bg-slate-800 border-slate-700 text-slate-300 hover:border-slate-500 disabled:opacity-50 transition-colors">↩ Unarchive</button>
          )}
          <button
            disabled={bulkBusy}
            onClick={() => { setSelectedIds(new Set()); setBulkError('') }}
            className="text-xs px-2 py-1 text-slate-500 hover:text-slate-300 transition-colors ml-auto"
          >
            Clear selection
          </button>
          {bulkError && <span className="text-xs text-red-400 w-full">{bulkError}</span>}
        </div>
      )}

      {showNewTask && workflow && (
        <NewTaskModal workflow={workflow} onClose={() => setShowNewTask(false)} />
      )}

      {loading ? (
        <div className="text-slate-400 text-sm">Loading…</div>
      ) : labels.length === 0 ? (
        <div className="text-slate-500 text-sm">
          No workflow configured. Add a repo and workflow first.
        </div>
      ) : (
        <div className="flex-1 min-h-0">
          <TaskBoard
            labels={labels}
            tasks={filteredTasks}
            runningTaskIds={runningTaskIds}
            rateLimitedTaskIds={rateLimitedTaskIds}
            onAddTask={() => setShowNewTask(true)}
            condensed={condensed}
            transitions={transitions}
            selectedIds={selectedIds}
            onToggleSelect={toggleSelect}
          />
        </div>
      )}
    </div>
  )
}
