import { useEffect, useState } from 'react'
import { useTasksStore } from '../stores/tasks'
import { useWorkflowStore } from '../stores/workflow'
import { useReposStore } from '../stores/repos'
import TaskBoard from '../components/board/TaskBoard'
import NewTaskModal from '../components/board/NewTaskModal'
import { api } from '../api/client'
import { wsClient } from '../api/ws'

const CONDENSED_STORAGE_KEY = 'board.condensed'

export default function BoardPage() {
  const { tasks, loading, fetch: fetchTasks, upsert } = useTasksStore()
  const { workflows, fetch: fetchWorkflows } = useWorkflowStore()
  const { fetch: fetchRepos } = useReposStore()
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
    fetchTasks()
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

  const workflow = workflows[0]
  const labels = workflow?.labels ?? []
  const transitions = workflow?.transitions ?? []

  return (
    <div className="p-6 h-full flex flex-col">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-slate-100">Board</h1>
        <div className="flex items-center gap-3">
          {workflow && (
            <span className="text-xs text-slate-500">Workflow: {workflow.name}</span>
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
            tasks={tasks}
            runningTaskIds={runningTaskIds}
            rateLimitedTaskIds={rateLimitedTaskIds}
            onAddTask={() => setShowNewTask(true)}
            condensed={condensed}
            transitions={transitions}
          />
        </div>
      )}
    </div>
  )
}
