import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useTasksStore } from './tasks'
import type { Task } from '../api/client'

const tasksListMock = vi.fn()

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      tasks: {
        ...actual.api.tasks,
        list: (...args: unknown[]) => tasksListMock(...args),
      },
    },
  }
})

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'A task',
    description: '',
    type: 'feature',
    label: 'todo',
    repo_id: 'r1',
    workflow_id: 'wf',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as Task
}

function page(items: Task[], nextCursor: string | null = null) {
  return { items, nextCursor }
}

beforeEach(() => {
  vi.clearAllMocks()
  useTasksStore.setState({
    tasks: [],
    loading: false,
    loaded: false,
    error: null,
    reqId: 0,
    upsertedDuringFetch: new Set(),
    removedDuringFetch: new Set(),
  })
})

describe('useTasksStore.fetch request sequencing', () => {
  it('discards an out-of-order (slower, earlier-started) fetch result', async () => {
    let resolveFirst!: (v: ReturnType<typeof page>) => void
    const firstPromise = new Promise<ReturnType<typeof page>>((res) => { resolveFirst = res })

    // Call #1 starts first but resolves after call #2.
    tasksListMock.mockImplementationOnce(() => firstPromise)
    tasksListMock.mockImplementationOnce(() => Promise.resolve(page([task({ id: 'from-call-2' })])))

    const p1 = useTasksStore.getState().fetch()
    const p2 = useTasksStore.getState().fetch()

    // Let call #2 finish first.
    await p2
    expect(useTasksStore.getState().tasks.map((t) => t.id)).toEqual(['from-call-2'])

    // Now let call #1's stale response land.
    resolveFirst(page([task({ id: 'from-call-1' })]))
    await p1

    // Call #1's result must not have overwritten call #2's.
    expect(useTasksStore.getState().tasks.map((t) => t.id)).toEqual(['from-call-2'])
  })

  it('sets loaded true after a successful fetch, and leaves it false on error', async () => {
    tasksListMock.mockResolvedValueOnce(page([task()]))
    await useTasksStore.getState().fetch()
    expect(useTasksStore.getState().loaded).toBe(true)

    useTasksStore.setState({ loaded: false })
    tasksListMock.mockRejectedValueOnce(new Error('boom'))
    await useTasksStore.getState().fetch()
    expect(useTasksStore.getState().loaded).toBe(false)
    expect(useTasksStore.getState().error).toContain('boom')
  })

  it('pages through multiple results via nextCursor', async () => {
    tasksListMock.mockImplementationOnce(() => Promise.resolve(page([task({ id: 'p1' })], 'cursor-1')))
    tasksListMock.mockImplementationOnce(() => Promise.resolve(page([task({ id: 'p2' })], null)))

    await useTasksStore.getState().fetch()

    expect(useTasksStore.getState().tasks.map((t) => t.id)).toEqual(['p1', 'p2'])
    expect(tasksListMock).toHaveBeenCalledTimes(2)
  })
})

describe('useTasksStore.fetch vs. concurrent WS upsert/remove', () => {
  it('keeps an upsert applied mid-fetch instead of reverting to the pre-upsert page data', async () => {
    let resolvePage!: (v: ReturnType<typeof page>) => void
    const pagePromise = new Promise<ReturnType<typeof page>>((res) => { resolvePage = res })
    tasksListMock.mockImplementationOnce(() => pagePromise)

    const fetchPromise = useTasksStore.getState().fetch()

    // A WS event upserts a newer version of a task that's also in the
    // in-flight page's (stale) result.
    useTasksStore.getState().upsert(task({ id: 't1', title: 'Updated via WS' }))

    // The page resolves with the pre-update version of the same task.
    resolvePage(page([task({ id: 't1', title: 'Stale from page' })]))
    await fetchPromise

    const t1 = useTasksStore.getState().tasks.find((t) => t.id === 't1')
    expect(t1?.title).toBe('Updated via WS')
  })

  it('keeps a mid-fetch remove applied instead of resurrecting the task from page data', async () => {
    let resolvePage!: (v: ReturnType<typeof page>) => void
    const pagePromise = new Promise<ReturnType<typeof page>>((res) => { resolvePage = res })
    tasksListMock.mockImplementationOnce(() => pagePromise)

    const fetchPromise = useTasksStore.getState().fetch()

    useTasksStore.getState().remove('t1')

    resolvePage(page([task({ id: 't1' })]))
    await fetchPromise

    expect(useTasksStore.getState().tasks.find((t) => t.id === 't1')).toBeUndefined()
  })

  it('prepends a task created mid-fetch that is absent from the page data', async () => {
    let resolvePage!: (v: ReturnType<typeof page>) => void
    const pagePromise = new Promise<ReturnType<typeof page>>((res) => { resolvePage = res })
    tasksListMock.mockImplementationOnce(() => pagePromise)

    const fetchPromise = useTasksStore.getState().fetch()

    useTasksStore.getState().upsert(task({ id: 'brand-new', title: 'Created mid-fetch' }))

    resolvePage(page([task({ id: 'existing' })]))
    await fetchPromise

    expect(useTasksStore.getState().tasks.map((t) => t.id)).toEqual(['brand-new', 'existing'])
  })

  it('upsert/remove outside of a fetch behave as plain synchronous list edits', () => {
    useTasksStore.setState({ tasks: [task({ id: 't1' })] })
    useTasksStore.getState().upsert(task({ id: 't1', title: 'Edited' }))
    expect(useTasksStore.getState().tasks[0].title).toBe('Edited')

    useTasksStore.getState().remove('t1')
    expect(useTasksStore.getState().tasks).toEqual([])
  })
})
