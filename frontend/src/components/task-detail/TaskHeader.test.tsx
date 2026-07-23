import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Task } from '../../api/client'
import type TaskHeaderType from './TaskHeader'
import TaskHeader from './TaskHeader'

// Regression test for #145 / #178 — attachment URLs must go through the same
// BASE_URL-aware base that src/api/client.ts's exported `BASE` constant uses,
// not a hardcoded `/api/v1/uploads/...` path (which 404'd under a non-root
// base like the production `/tasks/` base set in vite.config.ts).
//
// As of #178 attachments are fetched through the authed client
// (`authedRawFetch`) and rendered as object (`blob:`) URLs, so they also carry
// the Authorization header when API_TOKEN is set (#138). The regression
// assertion is therefore that `authedRawFetch` is invoked with a
// BASE_URL-prefixed uploads URL; the rendered <img src> is an opaque blob URL.
//
// `BASE` is computed once at module-evaluation time from
// `import.meta.env.BASE_URL`, so exercising a non-default BASE_URL requires
// `vi.stubEnv` + `vi.resetModules()` + a dynamic import *before* any other
// test in this file (or another file) has already imported client.ts/
// TaskHeader.tsx with the default env — see client.test.ts for the same
// pattern applied to client.ts directly. Because of this we deliberately do
// NOT `vi.mock` client.ts (a mock factory runs once and would freeze BASE at
// its first value); instead we stub the global `fetch` that the real
// `authedRawFetch` calls and assert on the URL it receives.

function baseTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'Test task',
    description: '',
    type: 'feature',
    label: 'todo',
    repo_id: 'r1',
    workflow_id: 'w1',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

const noop = () => {}

function renderHeader(TaskHeader: typeof TaskHeaderType, task: Task) {
  return render(
    <TaskHeader
      task={task}
      repos={[]}
      isStartingColumn={false}
      editingTask={false}
      editTitle=""
      setEditTitle={noop}
      editDesc=""
      setEditDesc={noop}
      editType=""
      setEditType={noop}
      editRepoId=""
      setEditRepoId={noop}
      editMaxCostUsd=""
      setEditMaxCostUsd={noop}
      editPriority={0}
      setEditPriority={noop}
      runs={[]}
      taskSaving={false}
      taskSaveError=""
      onStartEdit={noop}
      onCancelEdit={noop}
      onTaskSave={noop}
      onDelete={noop}
      onTogglePause={noop}
      actionPending={false}
      onCreatePR={noop}
      creatingPR={false}
      onSyncGitState={noop}
      onBack={noop}
      labels={[]}
      onMoveLabel={noop}
    />,
  )
}

describe('TaskHeader attachment URLs (#145 / #178)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.resetModules()
    // The real authedRawFetch calls global fetch — stub it with a blob response.
    fetchSpy = vi.fn(async () => ({ ok: true, blob: async () => new Blob(['x']) }) as Response)
    vi.stubGlobal('fetch', fetchSpy)
    // jsdom doesn't implement the object-URL API the component relies on.
    URL.createObjectURL = vi.fn(() => 'blob:mock-url')
    URL.revokeObjectURL = vi.fn()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  // The URL passed to fetch is the first positional argument.
  const fetchedUrls = () => fetchSpy.mock.calls.map((c) => c[0])

  // Production builds set `base: '/tasks/'` in vite.config.ts, so BASE_URL is
  // NOT always '/' — simulate that deployment shape here rather than relying
  // on the test runner's (root, '/') default, which would let this
  // regression test pass by accident.
  it('fetches attachments through a non-root BASE_URL via the authed client', async () => {
    vi.stubEnv('BASE_URL', '/tasks/')
    const { default: TaskHeader } = await import('./TaskHeader')

    renderHeader(TaskHeader, baseTask({ attachments: ['foo.png'] }))

    const img = (await screen.findByAltText('attachment')) as HTMLImageElement
    expect(fetchedUrls()).toContain('/tasks/api/v1/uploads/foo.png')
    expect(img.getAttribute('src')).toBe('blob:mock-url')
  })

  it('opens the blob URL in a new tab on click', async () => {
    vi.stubEnv('BASE_URL', '/tasks/')
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const { default: TaskHeader } = await import('./TaskHeader')

    renderHeader(TaskHeader, baseTask({ attachments: ['foo.png'] }))
    ;(await screen.findByAltText('attachment')).click()

    expect(openSpy).toHaveBeenCalledWith('blob:mock-url', '_blank')
  })

  it('resolves attachment URLs correctly at the default (root) BASE_URL', async () => {
    vi.stubEnv('BASE_URL', '/')
    const { default: TaskHeader } = await import('./TaskHeader')

    renderHeader(TaskHeader, baseTask({ attachments: ['foo.png'] }))

    await screen.findByAltText('attachment')
    expect(fetchedUrls()).toContain('/api/v1/uploads/foo.png')
  })
})

describe('TaskHeader agent notes modal', () => {
  it('opens a modal with the full notes when the preview is clicked, and closes it', async () => {
    const user = userEvent.setup()
    renderHeader(TaskHeader, baseTask({ agent_notes: 'Some detailed agent notes here.' }))

    // Preview button renders the notes text; the modal is not open yet.
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    await user.click(screen.getByTitle('Click to expand'))

    const dialog = await screen.findByRole('dialog', { name: 'Agent Notes' })
    expect(dialog).toBeInTheDocument()

    await user.click(screen.getByTitle('Close'))

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('does not render a clickable notes preview when there are no agent notes', () => {
    renderHeader(TaskHeader, baseTask({ agent_notes: '' }))
    expect(screen.queryByTitle('Click to expand')).not.toBeInTheDocument()
  })
})
