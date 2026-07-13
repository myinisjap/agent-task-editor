import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { Task } from '../../api/client'
import type TaskHeaderType from './TaskHeader'

// Regression test for #145 — attachment URLs used to hardcode
// `/api/v1/uploads/...` instead of going through the same BASE_URL-aware
// base that src/api/client.ts's exported `BASE` constant uses. When the app
// is served from a non-root base (e.g. the production `/tasks/` base set in
// vite.config.ts), the hardcoded path 404'd. Fixed in TaskHeader.tsx
// (imports `BASE` from client.ts instead of hardcoding the prefix).
//
// `BASE` is computed once at module-evaluation time from
// `import.meta.env.BASE_URL`, so exercising a non-default BASE_URL requires
// `vi.stubEnv` + `vi.resetModules()` + a dynamic import *before* any other
// test in this file (or another file) has already imported client.ts/
// TaskHeader.tsx with the default env — see client.test.ts for the same
// pattern applied to client.ts directly.
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
    />,
  )
}

describe('TaskHeader attachment URLs (#145)', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
  })

  // Production builds set `base: '/tasks/'` in vite.config.ts, so BASE_URL is
  // NOT always '/' — simulate that deployment shape here rather than relying
  // on the test runner's (root, '/') default, which would let this
  // regression test pass by accident.
  it('prefixes attachment <img src> with a non-root BASE_URL', async () => {
    vi.stubEnv('BASE_URL', '/tasks/')
    const { default: TaskHeader } = await import('./TaskHeader')
    const expectedPrefix = '/tasks/api/v1/uploads/'

    renderHeader(TaskHeader, baseTask({ attachments: ['foo.png'] }))

    const img = screen.getByAltText('attachment') as HTMLImageElement
    expect(img.getAttribute('src')).toBe(`${expectedPrefix}foo.png`)
  })

  it('opens the same BASE_URL-prefixed path on click', async () => {
    vi.stubEnv('BASE_URL', '/tasks/')
    const { default: TaskHeader } = await import('./TaskHeader')
    const expectedUrl = '/tasks/api/v1/uploads/foo.png'
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)

    renderHeader(TaskHeader, baseTask({ attachments: ['foo.png'] }))
    screen.getByAltText('attachment').click()

    expect(openSpy).toHaveBeenCalledWith(expectedUrl, '_blank')
    openSpy.mockRestore()
  })

  it('still resolves attachment URLs correctly at the default (root) BASE_URL', async () => {
    const { default: TaskHeader } = await import('./TaskHeader')

    renderHeader(TaskHeader, baseTask({ attachments: ['foo.png'] }))

    const img = screen.getByAltText('attachment') as HTMLImageElement
    expect(img.getAttribute('src')).toBe('/api/v1/uploads/foo.png')
  })
})
