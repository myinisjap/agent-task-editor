import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import RememberLessonModal from './RememberLessonModal'

const createLesson = vi.fn()
vi.mock('../../api/client', () => ({
  api: { repos: { createLesson: (...a: unknown[]) => createLesson(...a) } },
}))

describe('RememberLessonModal', () => {
  beforeEach(() => {
    createLesson.mockReset()
    vi.spyOn(window, 'alert').mockImplementation(() => {})
  })

  it('prefills body (trimmed to 300) and defaults kind to preference', () => {
    render(<RememberLessonModal repoId="r1" taskId="t1" text={'x'.repeat(400)} onClose={() => {}} onSaved={() => {}} />)
    expect(screen.getByLabelText('Lesson')).toHaveValue('x'.repeat(300))
    expect(screen.getByLabelText('Kind')).toHaveValue('preference')
    expect(screen.getByText(/Trimmed to 300/)).toBeInTheDocument()
  })

  it('creates an active human lesson linked to the task', async () => {
    const lesson = { id: 'l1' }
    createLesson.mockResolvedValue(lesson)
    const onSaved = vi.fn()
    render(<RememberLessonModal repoId="r1" taskId="t1" text="  use tabs " onClose={() => {}} onSaved={onSaved} />)
    await userEvent.click(screen.getByRole('button', { name: 'Save lesson' }))
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(lesson))
    expect(createLesson).toHaveBeenCalledWith('r1', {
      kind: 'preference', body: 'use tabs', path_globs: [], stages: [],
      status: 'active', source: 'human', source_task_id: 't1',
    })
  })

  it('shows errors inline and stays open', async () => {
    createLesson.mockRejectedValue(new Error('boom'))
    const onSaved = vi.fn()
    render(<RememberLessonModal repoId="r1" taskId="t1" text="x" onClose={() => {}} onSaved={onSaved} />)
    await userEvent.click(screen.getByRole('button', { name: 'Save lesson' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('boom')
    expect(onSaved).not.toHaveBeenCalled()
    expect(window.alert).not.toHaveBeenCalled()
  })
})
