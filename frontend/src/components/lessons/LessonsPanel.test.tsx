import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import LessonsPanel from './LessonsPanel'

const m = {
  repoLessons: vi.fn(), createLesson: vi.fn(), listGlobal: vi.fn(), createGlobal: vi.fn(), update: vi.fn(), del: vi.fn(),
}
vi.mock('../../api/client', () => ({
  api: {
    repos: { lessons: (...a: unknown[]) => m.repoLessons(...a), createLesson: (...a: unknown[]) => m.createLesson(...a) },
    lessons: {
      listGlobal: (...a: unknown[]) => m.listGlobal(...a),
      createGlobal: (...a: unknown[]) => m.createGlobal(...a),
      update: (...a: unknown[]) => m.update(...a),
      delete: (...a: unknown[]) => m.del(...a),
    },
  },
}))

function lesson(o: Record<string, unknown> = {}) {
  return {
    id: 'l1', repo_id: 'r1', kind: 'trap', body: 'Do not touch X', path_globs: ['backend/**'], stages: ['work'],
    citations: [], source: 'human', source_task_id: 't9', status: 'active', injected_count: 3,
    created_at: '', updated_at: '', last_checked_at: null, ...o,
  }
}

const renderPanel = (repoId: string | null = 'r1') => render(<MemoryRouter><LessonsPanel repoId={repoId} /></MemoryRouter>)

describe('LessonsPanel', () => {
  beforeEach(() => {
    Object.values(m).forEach((f) => f.mockReset())
    m.repoLessons.mockResolvedValue([lesson()])
  })

  it('lists lessons with kind, body, scope and injected count', async () => {
    renderPanel()
    expect(await screen.findByText('Do not touch X')).toBeInTheDocument()
    expect(screen.getByText('trap')).toBeInTheDocument()
    expect(screen.getByText(/Paths: backend\/\*\*/)).toBeInTheDocument()
    expect(screen.getByText(/Stages: work/)).toBeInTheDocument()
    expect(screen.getByText('Injected 3×')).toBeInTheDocument()
    expect(m.repoLessons).toHaveBeenCalledWith('r1', undefined)
  })

  it('uses the global endpoint without a repo and shows the empty state', async () => {
    m.listGlobal.mockResolvedValue([])
    renderPanel(null)
    expect(await screen.findByText('No lessons yet.')).toBeInTheDocument()
    expect(m.repoLessons).not.toHaveBeenCalled()
  })

  it('re-queries when the status filter changes', async () => {
    renderPanel()
    await screen.findByText('Do not touch X')
    m.repoLessons.mockResolvedValue([])
    await userEvent.selectOptions(screen.getByLabelText('Filter by status'), 'retired')
    await waitFor(() => expect(m.repoLessons).toHaveBeenLastCalledWith('r1', 'retired'))
    expect(await screen.findByText('No retired lessons.')).toBeInTheDocument()
  })

  it('adds a repo lesson', async () => {
    m.repoLessons.mockResolvedValue([])
    m.createLesson.mockResolvedValue(lesson({ id: 'l2', body: 'New one' }))
    renderPanel()
    await screen.findByText('No lessons yet.')
    await userEvent.click(screen.getByRole('button', { name: '+ Add lesson' }))
    await userEvent.type(screen.getByLabelText('Lesson'), 'New one')
    await userEvent.click(screen.getByRole('button', { name: 'Add lesson' }))
    expect(await screen.findByText('New one')).toBeInTheDocument()
    expect(m.createLesson).toHaveBeenCalledWith('r1', { kind: 'convention', body: 'New one', path_globs: [], stages: [], status: 'active', source: 'human' })
  })

  it('adds a global lesson via the global endpoint', async () => {
    m.listGlobal.mockResolvedValue([])
    m.createGlobal.mockResolvedValue(lesson({ id: 'g1', repo_id: null, body: 'Global' }))
    renderPanel(null)
    await screen.findByText('No lessons yet.')
    await userEvent.click(screen.getByRole('button', { name: '+ Add lesson' }))
    await userEvent.type(screen.getByLabelText('Lesson'), 'Global')
    await userEvent.click(screen.getByRole('button', { name: 'Add lesson' }))
    await screen.findByText('Global')
    expect(m.createGlobal).toHaveBeenCalled()
  })

  it('edits a lesson with parsed globs and stages', async () => {
    m.update.mockResolvedValue(lesson({ body: 'Changed', path_globs: ['a', 'b'], stages: [] }))
    renderPanel()
    await screen.findByText('Do not touch X')
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    const body = screen.getByLabelText('Lesson')
    await userEvent.clear(body)
    await userEvent.type(body, 'Changed')
    const globs = screen.getByLabelText('Path globs')
    await userEvent.clear(globs)
    await userEvent.type(globs, 'a, b')
    await userEvent.clear(screen.getByLabelText('Stages'))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('Changed')
    expect(m.update).toHaveBeenCalledWith('l1', { kind: 'trap', body: 'Changed', path_globs: ['a', 'b'], stages: [] })
  })

  it('retires a lesson', async () => {
    m.update.mockResolvedValue(lesson({ status: 'retired' }))
    renderPanel()
    await screen.findByText('Do not touch X')
    await userEvent.click(screen.getByRole('button', { name: 'Retire' }))
    expect(m.update).toHaveBeenCalledWith('l1', { status: 'retired' })
    expect(await screen.findByRole('button', { name: 'Reactivate' })).toBeInTheDocument()
  })

  it('requires confirmation to delete', async () => {
    m.del.mockResolvedValue(undefined)
    renderPanel()
    await screen.findByText('Do not touch X')
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(m.del).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(m.del).toHaveBeenCalledWith('l1'))
    expect(screen.queryByText('Do not touch X')).not.toBeInTheDocument()
  })

  it('shows row action errors inline', async () => {
    m.update.mockRejectedValue(new Error('nope'))
    renderPanel()
    await screen.findByText('Do not touch X')
    await userEvent.click(screen.getByRole('button', { name: 'Retire' }))
    const alert = await screen.findByRole('alert')
    expect(within(alert.closest('li')!).getByText('nope')).toBeInTheDocument()
  })

  it('shows load errors', async () => {
    m.repoLessons.mockRejectedValue(new Error('load failed'))
    renderPanel()
    expect(await screen.findByRole('alert')).toHaveTextContent('load failed')
  })
})
