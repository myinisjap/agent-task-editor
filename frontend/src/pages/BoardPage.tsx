import { useEffect, useState } from 'react'
import { useTasksStore } from '../stores/tasks'
import { useWorkflowStore } from '../stores/workflow'
import TaskBoard from '../components/board/TaskBoard'
import { api } from '../api/client'
import { wsClient } from '../api/ws'

export default function BoardPage() {
  const { tasks, loading, fetch: fetchTasks, upsert } = useTasksStore()
  const { workflows, fetch: fetchWorkflows } = useWorkflowStore()
  const [runningTaskIds] = useState(() => new Set<string>())

  useEffect(() => {
    fetchTasks()
    fetchWorkflows()
  }, [])

  useEffect(() => {
    const off = wsClient.on((event) => {
      if (event.type === 'task.label_changed' || event.type === 'task.updated' || event.type === 'task.created') {
        // Refresh the task from API to get latest data
        const taskId = event.type === 'task.created' ? event.payload.id :
                       event.type === 'task.updated' ? event.payload.id : event.payload.task_id
        api.tasks.get(taskId).then(upsert).catch(() => {})
      }
    })
    return off
  }, [upsert])

  const workflow = workflows[0]
  const labels = workflow?.labels ?? []

  return (
    <div className="p-6 h-full flex flex-col">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-slate-100">Board</h1>
        {workflow && (
          <span className="text-xs text-slate-500">Workflow: {workflow.name}</span>
        )}
      </div>

      {loading ? (
        <div className="text-slate-400 text-sm">Loading…</div>
      ) : labels.length === 0 ? (
        <div className="text-slate-500 text-sm">
          No workflow configured. Add a repo and workflow first.
        </div>
      ) : (
        <TaskBoard labels={labels} tasks={tasks} runningTaskIds={runningTaskIds} />
      )}
    </div>
  )
}
