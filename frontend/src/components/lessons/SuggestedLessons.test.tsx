import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import SuggestedLessons from './SuggestedLessons'

const m = { lessons: vi.fn(), runs: vi.fn(), distill: vi.fn(), update: vi.fn(), dismiss: vi.fn() }
vi.mock('../../api/client', () => ({
  api: {
    repos: {
      lessons: (...a: unknown[]) => m.lessons(...a),
      lessonDistillRuns: (...a: unknown[]) => m.runs(...a),
      distillLessons: (...a: unknown[]) => m.distill(...a),
    },
    lessons: { update: (...a: unknown[]) => m.update(...a), dismiss: (...a: unknown[]) => m.dismiss(...a) },
  },
}))

const proposal = {
  id: 'p1', repo_id: 'r1', kind: 'trap', body: 'Run make test', path_globs: [], stages: [],
  citations: [{ task_id: 't1', title: 'Fix build', kind: 'rejection', quote: 'use make test' }],
  source: 'distiller', source_task_id: 't1', status: 'proposed', injected_count: 0,
}

const renderIt = (onAccepted = vi.fn()) =>
  render(<MemoryRouter><SuggestedLessons repoId="r1" onAccepted={onAccepted} /></MemoryRouter>)

describe('SuggestedLessons', () => {
  beforeEach(() => {
    Object.values(m).forEach((f) => f.mockReset())
    m.lessons.mockResolvedValue([proposal])
    m.runs.mockResolvedValue([{ id: 'x', status: 'succeeded', started_at: '2026-01-01T00:00:00Z', cost_usd: 0.12, error: '' }])
  })

  it('shows proposals with linked evidence, last run and cost', async () => {
    renderIt()
    expect(await screen.findByText('Run make test')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Fix build' })).toHaveAttribute('href', '/tasks/t1')
    expect(screen.getByText('use make test')).toBeInTheDocument()
    expect(screen.getByText(/\$0\.12/)).toBeInTheDocument()
  })

  it('accepts by setting status active', async () => {
    m.update.mockResolvedValue({ ...proposal, status: 'active' })
    const onAccepted = vi.fn()
    renderIt(onAccepted)
    await userEvent.click(await screen.findByRole('button', { name: 'Accept' }))
    await waitFor(() => expect(m.update).toHaveBeenCalledWith('p1', { status: 'active' }))
    expect(onAccepted).toHaveBeenCalled()
    expect(screen.queryByText('Run make test')).not.toBeInTheDocument()
  })

  it('dismisses', async () => {
    m.dismiss.mockResolvedValue(undefined)
    renderIt()
    await userEvent.click(await screen.findByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(m.dismiss).toHaveBeenCalledWith('p1'))
    expect(screen.queryByText('Run make test')).not.toBeInTheDocument()
  })

  it('shows the error when no provider is configured', async () => {
    m.distill.mockRejectedValue(new Error('no provider config is set'))
    renderIt()
    await userEvent.click(await screen.findByRole('button', { name: 'Suggest lessons' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no provider config')
  })
})
