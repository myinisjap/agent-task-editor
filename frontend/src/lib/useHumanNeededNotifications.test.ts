import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { Task, Workflow } from '../api/client'
import { useTasksStore } from '../stores/tasks'
import { useWorkflowStore } from '../stores/workflow'
import { useNotificationsStore } from '../stores/notifications'

let wsHandler: ((event: unknown) => void) | undefined
const wsUnsubscribe = vi.fn()
const wsOn = vi.fn((handler: (event: unknown) => void) => {
  wsHandler = handler
  return wsUnsubscribe
})

vi.mock('../api/ws', () => ({
  wsClient: { on: (h: (event: unknown) => void) => wsOn(h) },
}))

const tasksGetMock = vi.fn()
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      tasks: { get: (...args: unknown[]) => tasksGetMock(...args) },
    },
  }
})

class FakeNotification {
  static permission: NotificationPermission = 'granted'
  static instances: FakeNotification[] = []
  title: string
  body?: string
  tag?: string
  onclick: (() => void) | null = null
  constructor(title: string, options?: NotificationOptions) {
    this.title = title
    this.body = options?.body
    this.tag = options?.tag
    FakeNotification.instances.push(this)
  }
}

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'My task',
    description: '',
    type: 'feature',
    label: 'review',
    repo_id: 'r1',
    workflow_id: 'wf1',
    ...overrides,
  } as Task
}

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf1',
    name: 'wf',
    description: '',
    labels: [
      { id: 'l1', workflow_id: 'wf1', name: 'review', color: '#000', sort_order: 0, agent_ignore: 1, is_terminal: 0 },
      { id: 'l2', workflow_id: 'wf1', name: 'done', color: '#000', sort_order: 1, agent_ignore: 0, is_terminal: 1 },
    ],
    transitions: [],
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('useHumanNeededNotifications', () => {
  beforeEach(() => {
    wsHandler = undefined
    wsOn.mockClear()
    wsUnsubscribe.mockClear()
    tasksGetMock.mockReset()
    FakeNotification.instances = []
    vi.stubGlobal('Notification', FakeNotification)
    useNotificationsStore.setState({ enabled: true, permission: 'granted' })
    useTasksStore.setState({ tasks: [], loading: false, error: null })
    useWorkflowStore.setState({ workflows: [], loading: false, selectedId: null })
  })

  it('registers and cleans up a single ws handler', async () => {
    const { useHumanNeededNotifications } = await import('./useHumanNeededNotifications')
    const { unmount } = renderHook(() => useHumanNeededNotifications())

    expect(wsOn).toHaveBeenCalledTimes(1)
    expect(wsUnsubscribe).not.toHaveBeenCalled()

    unmount()
    expect(wsUnsubscribe).toHaveBeenCalledTimes(1)
  })

  it('shows a notification on task.needs_human using the cached task title', async () => {
    const { useHumanNeededNotifications } = await import('./useHumanNeededNotifications')
    useTasksStore.setState({ tasks: [task({ id: 'needs-human-1' })], loading: false, error: null })

    renderHook(() => useHumanNeededNotifications())
    expect(wsHandler).toBeDefined()

    await wsHandler!({ type: 'task.needs_human', payload: { task_id: 'needs-human-1', run_id: 'r1', message: 'stuck' } })
    await Promise.resolve()
    await Promise.resolve()

    expect(FakeNotification.instances).toHaveLength(1)
    expect(FakeNotification.instances[0].body).toContain('My task')
    expect(FakeNotification.instances[0].body).toContain('stuck')
  })

  it('shows a notification when a task lands on a human-gate label', async () => {
    const { useHumanNeededNotifications } = await import('./useHumanNeededNotifications')
    useTasksStore.setState({ tasks: [task({ id: 'gate-1' })], loading: false, error: null })
    useWorkflowStore.setState({ workflows: [workflow()], loading: false, selectedId: 'wf1' })

    renderHook(() => useHumanNeededNotifications())
    await wsHandler!({ type: 'task.label_changed', payload: { task_id: 'gate-1', from: 'todo', to: 'review' } })
    await Promise.resolve()
    await Promise.resolve()

    expect(FakeNotification.instances).toHaveLength(1)
    expect(FakeNotification.instances[0].body).toContain('review')
  })

  it('does not notify when the destination label is not a human gate', async () => {
    const { useHumanNeededNotifications } = await import('./useHumanNeededNotifications')
    useTasksStore.setState({ tasks: [task({ id: 'not-gate-1' })], loading: false, error: null })
    useWorkflowStore.setState({ workflows: [workflow()], loading: false, selectedId: 'wf1' })

    renderHook(() => useHumanNeededNotifications())
    await wsHandler!({ type: 'task.label_changed', payload: { task_id: 'not-gate-1', from: 'review', to: 'done' } })
    await Promise.resolve()
    await Promise.resolve()

    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('fetches the workflow list when empty so the human-gate check still works', async () => {
    const { useHumanNeededNotifications } = await import('./useHumanNeededNotifications')
    useTasksStore.setState({ tasks: [task({ id: 'fetch-wf-1' })], loading: false, error: null })
    // workflows store empty; stub fetch() to populate it
    const fetchSpy = vi.spyOn(useWorkflowStore.getState(), 'fetch').mockImplementation(async () => {
      useWorkflowStore.setState({ workflows: [workflow()], loading: false, selectedId: 'wf1' })
    })

    renderHook(() => useHumanNeededNotifications())
    await wsHandler!({ type: 'task.label_changed', payload: { task_id: 'fetch-wf-1', from: 'todo', to: 'review' } })
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()

    expect(fetchSpy).toHaveBeenCalled()
    expect(FakeNotification.instances).toHaveLength(1)
  })
})
