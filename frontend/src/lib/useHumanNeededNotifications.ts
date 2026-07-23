import { useEffect } from 'react'
import { wsClient, type WSEvent } from '../api/ws'
import { api } from '../api/client'
import { useTasksStore } from '../stores/tasks'
import { useWorkflowStore } from '../stores/workflow'
import { showHumanNeededNotification } from './notify'
import { isHumanGateLabel } from './humanGate'

/** Looks up a task's title from the already-loaded tasks store, falling back
 *  to a REST fetch (the store is only populated once the board/dashboard has
 *  been visited this session). Never throws — callers just want a title. */
async function resolveTaskTitle(taskId: string): Promise<string> {
  const cached = useTasksStore.getState().tasks.find((t) => t.id === taskId)
  if (cached) return cached.title
  try {
    const task = await api.tasks.get(taskId)
    return task.title
  } catch {
    return 'A task'
  }
}

/** Finds the workflow a task belongs to. Tries the tasks store first (has
 *  workflow_id without an extra request), else fetches the task, then looks
 *  the workflow up in the workflow store (fetching the list if empty so this
 *  works even before the user has visited the Workflow page). */
async function resolveTaskWorkflow(taskId: string) {
  let workflowId = useTasksStore.getState().tasks.find((t) => t.id === taskId)?.workflow_id
  if (!workflowId) {
    try {
      const task = await api.tasks.get(taskId)
      workflowId = task.workflow_id
    } catch {
      return undefined
    }
  }

  let { workflows } = useWorkflowStore.getState()
  if (workflows.length === 0) {
    await useWorkflowStore.getState().fetch()
    workflows = useWorkflowStore.getState().workflows
  }
  return workflows.find((w) => w.id === workflowId)
}

async function handleNeedsHuman(payload: Extract<WSEvent, { type: 'task.needs_human' }>['payload']) {
  const title = await resolveTaskTitle(payload.task_id)
  showHumanNeededNotification({
    title: 'Human needed',
    body: `${title}: ${payload.message}`,
    taskId: payload.task_id,
  })
}

async function handleLabelChanged(payload: Extract<WSEvent, { type: 'task.label_changed' }>['payload']) {
  const workflow = await resolveTaskWorkflow(payload.task_id)
  if (!isHumanGateLabel(workflow, payload.to)) return

  const title = await resolveTaskTitle(payload.task_id)
  showHumanNeededNotification({
    title: 'Human needed',
    body: `${title} is waiting on "${payload.to}" (human review)`,
    taskId: payload.task_id,
  })
}

/**
 * Registers a single app-wide WebSocket handler that fires an opt-in browser
 * notification whenever a task needs a human — either because an agent
 * explicitly asked for one (MCP request_human / cost-budget or retry
 * exhaustion, all surfaced as task.needs_human), or because a task landed on
 * a label only a human can move it out of (derived from task.label_changed +
 * the active workflow's transitions). Mount once near the app root so it's
 * registered regardless of route. No-ops internally unless the user has
 * opted in and granted permission (see stores/notifications.ts).
 */
export function useHumanNeededNotifications(): void {
  useEffect(() => {
    const unsubscribe = wsClient.on((event) => {
      if (event.type === 'task.needs_human') {
        void handleNeedsHuman(event.payload)
      } else if (event.type === 'task.label_changed') {
        void handleLabelChanged(event.payload)
      }
    })
    return unsubscribe
  }, [])
}
